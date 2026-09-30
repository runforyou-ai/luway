//go:build server

// Package pgerr 提供 PostgreSQL 驱动错误的判定辅助。
package pgerr

import (
	"errors"

	"github.com/uptrace/bun/driver/pgdriver"
)

// uniqueViolationCode 是 PostgreSQL 唯一约束冲突的 SQLSTATE 代码。
const uniqueViolationCode = "23505"

// UniqueViolation 判断错误是否为唯一约束冲突，并返回触发的约束名。
func UniqueViolation(err error) (string, bool) {
	var postgresError pgdriver.Error
	if !errors.As(err, &postgresError) || postgresError.Field('C') != uniqueViolationCode {
		return "", false
	}
	return postgresError.Field('n'), true
}

// UniqueViolationOn 判断错误是否命中指定名称的唯一约束。
func UniqueViolationOn(err error, constraint string) bool {
	name, ok := UniqueViolation(err)
	return ok && name == constraint
}
