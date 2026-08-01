package httpapi

import (
	"io/fs"
	"net/http"

	"github.com/reportportal/service-marketplace/web"
)

func operatorFileServer() http.Handler {
	sub, err := fs.Sub(web.OperatorFS, "operator")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
