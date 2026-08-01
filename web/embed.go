package web

import "embed"

// OperatorFS is the Operator console static assets (source of truth: web/operator/).
//
//go:embed all:operator
var OperatorFS embed.FS
