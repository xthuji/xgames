// Package web 内嵌前端构建产物（vite build 输出 dist/，构建脚本先构建前端）。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
