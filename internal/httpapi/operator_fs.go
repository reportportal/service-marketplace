package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:operator
var operatorFS embed.FS

func operatorFileServer() http.Handler {
	sub, err := fs.Sub(operatorFS, "operator")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
