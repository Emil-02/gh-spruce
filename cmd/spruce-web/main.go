// Command spruce-web runs the web service locally:
//
//	GITHUB_TOKEN=$(gh auth token) go run ./cmd/spruce-web
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/Emil-02/gh-spruce/web"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	static := flag.String("public", "public", "directory with static files")
	flag.Parse()

	mux := http.NewServeMux()
	mux.Handle("/api/", web.Default())
	mux.Handle("/u/", web.Default())
	mux.Handle("/", http.FileServer(http.Dir(*static)))
	log.Printf("gh-spruce web on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
