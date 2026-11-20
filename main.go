// HyperShift Quest — a Kahoot-style multiplayer quiz server for the
// HyperShift question bank. One host runs it on the company network;
// players join from a browser and race tokens across the quest board.
//
// Usage:
//
//	go run .                       # embedded bank, defaults
//	go run . -config quest.toml    # configuration file
//	go run . -addr :8080           # flag overrides config
//
// See quest.toml.example for all configuration options.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed bank
var embeddedBank embed.FS

//go:embed web/app.html
var appHTML []byte

// Config is the quest.toml configuration. Every field has a working
// default; the file itself is optional.
type Config struct {
	Addr string `toml:"addr"`
	Bank struct {
		// Path points at a local HyperShift (hs-main) checkout. The
		// question bank is then loaded live from
		// skills/hypershift-quiz/questions/*.yaml, so the server always
		// quizzes whatever branch is checked out there. Accepts either
		// the repository root or the questions directory itself.
		// Empty means: use the bank embedded in this binary.
		Path string `toml:"path"`
	} `toml:"bank"`
	Game struct {
		Rounds             int      `toml:"rounds"`               // questions per game; 0 = all
		SecondsPerQuestion int      `toml:"seconds_per_question"` // timer per question
		Formats            []string `toml:"formats"`              // deck filter, e.g. ["mcq"]; empty = all
	} `toml:"game"`
}

func defaultConfig() Config {
	var c Config
	c.Addr = ":8080"
	c.Game.Rounds = 15
	c.Game.SecondsPerQuestion = 30
	return c
}

func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	if path == "" {
		if _, err := os.Stat("quest.toml"); err == nil {
			path = "quest.toml"
		} else {
			return c, nil
		}
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if c.Game.SecondsPerQuestion <= 0 {
		c.Game.SecondsPerQuestion = 30
	}
	return c, nil
}

func main() {
	configPath := flag.String("config", "", "path to quest.toml (default: ./quest.toml if present)")
	addr := flag.String("addr", "", "listen address, overrides config")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *addr != "" {
		cfg.Addr = *addr
	}

	questions, board, source, err := LoadBank(cfg.Bank.Path)
	if err != nil {
		log.Fatal(err)
	}
	mcq := 0
	for _, q := range questions {
		if q.Format == "mcq" {
			mcq++
		}
	}
	log.Printf("HyperShift Quest: %d questions (%d mcq) loaded from %s", len(questions), mcq, source)

	hub := NewHub(questions, board, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", serveApp)
	mux.HandleFunc("GET /host", serveApp)
	mux.HandleFunc("GET /board", serveApp)

	mux.HandleFunc("POST /api/rooms", jsonHandler(hub.CreateRoom))
	mux.HandleFunc("POST /api/rooms/{code}/join", jsonHandler(hub.Join))
	mux.HandleFunc("GET /api/rooms/{code}/state", jsonHandler(hub.State))
	mux.HandleFunc("GET /api/rooms/{code}/events", hub.Events)
	mux.HandleFunc("POST /api/rooms/{code}/answer", jsonHandler(hub.Answer))
	mux.HandleFunc("POST /api/rooms/{code}/host/{action}", jsonHandler(hub.HostAction))
	mux.HandleFunc("GET /api/rooms/{code}/hostview", jsonHandler(hub.HostView))
	mux.HandleFunc("GET /api/rooms/{code}/grading", jsonHandler(hub.GradingQueue))
	mux.HandleFunc("POST /api/rooms/{code}/grade", jsonHandler(hub.Grade))
	mux.HandleFunc("POST /api/rooms/{code}/complete-grading", jsonHandler(hub.CompleteGrading))

	log.Printf("listening on %s — players: http://<this-host>%s/  host: /host  board: /board", hostLabel(cfg.Addr), portLabel(cfg.Addr))
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func serveApp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(appHTML)
}

// jsonHandler adapts hub methods returning (any, error) to HTTP.
func jsonHandler(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload, err := fn(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
	}
}

func hostLabel(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		host, _ := os.Hostname()
		return host
	}
	return addr
}

func portLabel(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return addr
	}
	return ""
}

// The hub keeps idle rooms from accumulating forever during long-lived
// deployments. Game nights on a laptop can just restart the binary.
const roomIdleTimeout = 2 * time.Hour

var cleanupOnce sync.Once
