package main

import (
	"embed"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"

	sloghttp "github.com/samber/slog-http"
)

//go:embed urls.csv
var fs embed.FS
var urls = make(map[string]string)
var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
var port = 3000
var notFoundText = http.StatusText(http.StatusNotFound)

func main() {
	if err := loadURLS(); err != nil {
		panic(err)
	}

	log.Printf("loaded %d urls", len(urls))

	addr := fmt.Sprintf(":%d", port)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", status)
	mux.HandleFunc("GET /", resolve)

	handler := sloghttp.Recovery(sloghttp.New(logger)(mux))

	log.Printf("server listening at %s", addr)

	http.ListenAndServe(addr, handler)
}

func status(w http.ResponseWriter, r *http.Request) {
	j, _ := json.Marshal(struct {
		Status string `json:"status"`
	}{
		Status: "up",
	})
	w.Header().Add("Content-Type", "application/json")
	w.Write(j)
}

func resolve(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("url")
	if dst, ok := urls[src]; ok {
		http.Redirect(w, r, dst, http.StatusMovedPermanently)
	} else {
		http.Error(w, notFoundText, http.StatusNotFound)
	}
}

func loadURLS() error {
	f, err := fs.Open("urls.csv")
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		urls[row[0]] = row[1]
	}
	return nil
}
