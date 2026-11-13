// Package handler is the Vercel entry point; every route is rewritten here.
package handler

import (
	"net/http"

	"github.com/Emil-02/gh-spruce/internal/web"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	web.Default().ServeHTTP(w, r)
}
