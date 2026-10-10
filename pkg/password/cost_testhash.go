//go:build testhash

package password

import "golang.org/x/crypto/bcrypt"

// hashCost 是 testhash 构建标签下的密码哈希强度，取 bcrypt 允许的最小值，仅供测试使用。
const hashCost = bcrypt.MinCost
