#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077

# 每个任务使用独立钥匙串，结束时恢复原搜索列表。
for name in APPLE_CERTIFICATE_P12 APPLE_CERTIFICATE_PASSWORD APPLE_API_KEY_P8 APPLE_API_KEY_ID APPLE_API_ISSUER_ID; do
  if [[ -z "${!name:-}" ]]; then
    echo "Missing signing secret: $name" >&2
    exit 1
  fi
done
app=${1:?Application bundle required}
executor=${2:?Executor binary required}
output=${3:?Output directory required}
version=${4:?Version required}
arch=${5:?Architecture required}
slug=$(basename "$app" .app)
test -d "$app"
test -f "$executor"
mkdir -p "$output" "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization-reports"
temporary=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/apple-signing.XXXXXX")
keychain="$temporary/signing.keychain-db"
original_keychains=()
while IFS= read -r existing; do
  original_keychains+=("$existing")
done < <(security list-keychains -d user | sed -E 's/^[[:space:]]*"(.*)"$/\1/')
cleanup() {
  security list-keychains -d user -s "${original_keychains[@]}" >/dev/null 2>&1 || true
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  rm -rf "$temporary"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf '%s' "$APPLE_CERTIFICATE_P12" | base64 --decode > "$temporary/certificate.p12"
printf '%s' "$APPLE_API_KEY_P8" > "$temporary/notary.p8"
keychain_password=$(openssl rand -hex 32)
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 7200 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
# codesign 在新建托管机器上仍需通过搜索列表发现私钥。
security list-keychains -d user -s "$keychain" "${original_keychains[@]}"
security import "$temporary/certificate.p12" -k "$keychain" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign >/dev/null
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$keychain_password" "$keychain" >/dev/null
identities=$(security find-identity -v -p codesigning "$keychain" | awk '/"Developer ID Application:/ {print $2}')
if [[ $(printf '%s\n' "$identities" | awk 'NF {n++} END {print n+0}') != 1 ]]; then
  echo "Expected one valid Developer ID Application identity" >&2
  exit 1
fi
identity=$identities
unset APPLE_CERTIFICATE_P12 APPLE_CERTIFICATE_PASSWORD APPLE_API_KEY_P8 keychain_password
notary_auth=(--key "$temporary/notary.p8" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID")

notarize() {
  local archive=$1 label=$2 result="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization-reports/$2.json" submission status code=0
  xcrun notarytool submit "$archive" "${notary_auth[@]}" --output-format json > "$result"
  cat "$result"
  submission=$(jq -er '.id' "$result")
  xcrun notarytool wait "$submission" "${notary_auth[@]}" --timeout 20m --output-format json > "${result%.json}-status.json" || code=$?
  if [[ "$code" != 0 ]]; then
    # 等待超时不代表 Apple 拒绝；重新查询真实状态，便于继续跟踪原提交。
    if xcrun notarytool info "$submission" "${notary_auth[@]}" --output-format json > "$temporary/notary-status.json"; then
      cp "$temporary/notary-status.json" "${result%.json}-status.json"
    fi
  fi
  cat "${result%.json}-status.json"
  status=$(jq -r '.status // empty' "${result%.json}-status.json")
  if [[ "$status" == 'In Progress' ]]; then
    echo "Apple is still processing submission $submission; publication remains blocked. Query this submission before retrying." >&2
    exit 1
  fi
  if [[ "$status" != Accepted ]]; then
    if [[ -n "$submission" ]]; then
      xcrun notarytool log "$submission" "${notary_auth[@]}" "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization-reports/$label-log.json" || true
      if [[ -f "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization-reports/$label-log.json" ]]; then cat "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization-reports/$label-log.json"; fi
    fi
    echo "Notarization did not complete successfully: $label ($status)" >&2
    exit 1
  fi
  # 成功日志也要保留，用票据中的代码哈希检查嵌套产物是否全部被扫描。
  xcrun notarytool log "$submission" "${notary_auth[@]}" "${result%.json}-log.json"
  cat "${result%.json}-log.json"
}

# 内嵌代码由内向外签名，最后签应用包；启用 Hardened Runtime 和安全时间戳。
xattr -cr "$app"
while IFS= read -r -d '' item; do
  if [[ -f "$item" ]] && file -b "$item" | grep -q 'Mach-O'; then
    codesign --force --sign "$identity" --keychain "$keychain" --options runtime --timestamp "$item"
  elif [[ -d "$item" ]]; then
    case "$item" in
      *.framework|*.xpc|*.appex|*.app) codesign --force --sign "$identity" --keychain "$keychain" --options runtime --timestamp "$item" ;;
    esac
  fi
done < <(find "$app/Contents" -depth \( -type f -o -type d \) -print0)
codesign --force --sign "$identity" --keychain "$keychain" --options runtime --timestamp "$app"
codesign --force --sign "$identity" --keychain "$keychain" --options runtime --timestamp "$executor"
codesign --verify --deep --strict --verbose=2 "$app"
codesign --verify --strict --verbose=2 "$executor"

# 先生成并签名最终 DMG；公证通过后不再重建或重新签名它。
wails3 task darwin:create:dmg
dmg="$(dirname "$app")/$slug.dmg"
test -f "$dmg"
codesign --force --sign "$identity" --keychain "$keychain" --timestamp "$dmg"
codesign --verify --strict --verbose=2 "$dmg"

# Apple 会扫描 ZIP 内的 DMG 及其应用；独立执行器一起提交，只等待一轮。
mkdir "$temporary/payload"
cp "$dmg" "$temporary/payload/$slug.dmg"
cp "$executor" "$temporary/payload/$slug-executor"
ditto -c -k --keepParent "$temporary/payload" "$temporary/submission.zip"
notarize "$temporary/submission.zip" release
for item in "$app" "$executor" "$dmg"; do
  cdhash=$(codesign -d --verbose=4 "$item" 2>&1 | awk -F= '/^CDHash=/ {print $2}')
  if [[ -z "$cdhash" ]] || ! jq -e --arg hash "$cdhash" 'any(.ticketContents[]?; .cdhash == $hash)' "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarization-reports/release-log.json" >/dev/null; then
    echo "Notarization ticket does not cover: $item" >&2
    exit 1
  fi
done
xcrun stapler staple "$app"
xcrun stapler validate "$app"
spctl --assess --type execute --verbose=2 "$app"

# DMG 的外层票据覆盖内部应用；自动更新 ZIP 单独携带已附加票据的应用。
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
codesign --verify --strict --verbose=2 "$dmg"
spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"
cp "$dmg" "$output/${slug}_${version}_darwin_${arch}.dmg"
ditto -c -k --norsrc --noextattr --keepParent "$app" "$output/${slug}_${version}_darwin_${arch}.zip"

# 验证更新包解压后仍保有签名和公证票据。
ditto -x -k "$output/${slug}_${version}_darwin_${arch}.zip" "$temporary/unpacked"
codesign --verify --deep --strict "$temporary/unpacked/$slug.app"
xcrun stapler validate "$temporary/unpacked/$slug.app"
spctl --assess --type execute --verbose=2 "$temporary/unpacked/$slug.app"
mkdir "$temporary/executor"
cp "$executor" "$temporary/executor/$slug-executor"
tar -C "$temporary/executor" -czf "$output/${slug}-executor_${version}_darwin_${arch}.tar.gz" "$slug-executor"
