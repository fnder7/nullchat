package web

import (
	"embed"
	"net/http"
)

//go:embed index.html app.js style.css favicon.svg
var assets embed.FS

func Handler() http.Handler { return http.FileServer(http.FS(assets)) }
