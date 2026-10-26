// acd is the anti-cheat daemon: a JSON-over-HTTP ingest in front of the engine, and a read API for verdicts.
//
//	POST /v1/telemetry        one Batch or an array of them        -> 202 {"accepted": n}
//	GET  /v1/players/{id}     the player's latest Verdict           -> 200 / 404
//	GET  /healthz
//
// Only the game server should reach this port (private network or mTLS at the proxy): a client that can post its
// own telemetry can also post a clean one.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/engine"
	"github.com/Ederjoel26/argus/pkg/telemetry"
)

const maxBody = 4 << 20

func main() {
	configPath := flag.String("config", "config/anticheat.config.json", "game profile")
	listen := flag.String("listen", "127.0.0.1:8089", "address of the HTTP ingest")
	workers := flag.Int("workers", 0, "shards (0: one per CPU)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	verdicts := engine.NewMemorySink()
	eng := engine.New(cfg, engine.MultiSink{verdicts, engine.LogSink{Log: log}}, engine.Options{Workers: *workers})
	eng.Start(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/telemetry", func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&raw); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var batches []*telemetry.Batch
		if len(raw) > 0 && raw[0] == '[' {
			err = json.Unmarshal(raw, &batches)
		} else {
			var one telemetry.Batch
			err = json.Unmarshal(raw, &one)
			batches = []*telemetry.Batch{&one}
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		submitCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, b := range batches {
			if err := eng.Submit(submitCtx, b); err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, context.DeadlineExceeded) {
					status = http.StatusServiceUnavailable // every shard queue full: the transport should retry
				}
				http.Error(w, err.Error(), status)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]int{"accepted": len(batches)})
	})
	mux.HandleFunc("GET /v1/players/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := verdicts.Get(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]uint64{"accepted": eng.Accepted.Load(), "rejected": eng.Rejected.Load()})
	})

	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("acd listening", "addr", *listen, "tick_rate", cfg.TickRate, "weapons", len(cfg.WeaponProfiles))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http", "err", err)
		os.Exit(1)
	}
	eng.Wait()
}
