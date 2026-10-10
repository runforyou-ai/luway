//go:build !testhash

package password

import "golang.org/x/crypto/bcrypt"

// hashCost 是密码哈希强度。
const hashCost = bcrypt.DefaultCost
