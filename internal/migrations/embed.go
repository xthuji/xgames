// Package migrations 内嵌 SQL 迁移脚本，供 store 层启动时执行。
package migrations

import "embed"

// FS 按文件名顺序执行的全部迁移脚本
//
//go:embed *.sql
var FS embed.FS
