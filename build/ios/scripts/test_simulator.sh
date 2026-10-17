#!/bin/bash
# 在本次创建的模拟器中验证应用启动，保存截图与日志后清理设备。
set -euo pipefail

app="${1:?app bundle required}"
evidence="${2:?evidence directory required}"
mkdir -p "$evidence"
bundle_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Info.plist")
xcrun simctl list runtimes -j > "$evidence/runtimes.json"
selection=$(node - "$evidence/runtimes.json" <<'NODE'
const fs = require('node:fs');
const runtimes = JSON.parse(fs.readFileSync(process.argv[2], 'utf8')).runtimes
  .filter(runtime => runtime.isAvailable && runtime.identifier.includes('.iOS-'))
  .sort((a, b) => b.version.localeCompare(a.version, undefined, { numeric: true }));
const runtime = runtimes[0];
const device = runtime?.supportedDeviceTypes?.find(type => type.productFamily === 'iPhone');
if (!device) throw new Error('Install an available iOS simulator runtime in Xcode first');
console.log(`${runtime.identifier} ${device.identifier}`);
NODE
)
read -r runtime device_type <<< "$selection"
device=$(xcrun simctl create "CI-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$$" "$device_type" "$runtime")

# cleanup 只清理本次创建的设备，并保留原始测试退出状态。
cleanup() {
  status=$?
  trap - EXIT
  xcrun simctl spawn "$device" log show --last 2m --style compact > "$evidence/simulator.log" 2>&1 || true
  xcrun simctl shutdown "$device" || true
  xcrun simctl delete "$device" || true
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Runtime: %s\nDevice: %s\nBundle: %s\n' "$runtime" "$device" "$bundle_id" | tee "$evidence/result.txt"
xcrun simctl boot "$device"
node - "$device" <<'NODE'
const { spawnSync } = require('node:child_process');
const result = spawnSync('xcrun', ['simctl', 'bootstatus', process.argv[2], '-b'], {
  stdio: 'inherit', timeout: 300_000,
});
if (result.error) console.error(result.error.message);
process.exit(result.status ?? 1);
NODE
xcrun simctl install "$device" "$app"
launch=$(xcrun simctl launch --terminate-running-process "$device" "$bundle_id")
printf '%s\n' "$launch" | tee -a "$evidence/result.txt"
pid="${launch##*: }"
[[ "$pid" =~ ^[0-9]+$ ]]
# 持续观察启动后十秒，崩溃或退出时立即失败。
for ((attempt=0; attempt<10; attempt++)); do
  kill -0 "$pid"
  sleep 1
done
xcrun simctl io "$device" screenshot "$evidence/startup.png"
echo 'PASS: installed, launched, and remained running for 10 seconds' | tee -a "$evidence/result.txt"
