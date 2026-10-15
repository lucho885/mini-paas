// Package main es el punto de entrada. En Go, un ejecutable siempre es
// "package main" con una función main() — equivalente a tu index.ts.
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"mini-paas/internal/api"
	"mini-paas/internal/app"

	// El guion bajo importa el paquete solo por su efecto secundario: al
	// cargarse, el driver se registra a sí mismo bajo el nombre "pgx" en
	// database/sql. No usamos nada de él directamente.
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// slog es el logger estructurado de la librería estándar.
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://paas:paas@localhost:5432/paas"
	}

	// sql.Open NO conecta todavía: solo prepara el pool. Por eso hacemos Ping.
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		logger.Error("abrir base de datos", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// context.WithTimeout crea un contexto que se cancela solo a los 5s.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logger.Error("conectar a postgres", "error", err)
		os.Exit(1)
	}
	logger.Info("conectado a postgres")

	// ServeMux es el router. Desde Go 1.22 acepta "MÉTODO /ruta" en el patrón,
	// así que no necesitas Express ni chi para rutas básicas.
	mux := http.NewServeMux()

	// Un handler es una función (w, r): w es donde escribes la respuesta,
	// r es la petición. Equivale a (req, res) en Express, pero en orden inverso.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	// Cableado de dependencias: repositorio -> handler -> rutas.
	apps := app.NewRepository(db)
	api.NewHandler(apps, logger).Register(mux)

	addr := ":8080"
	logger.Info("servidor escuchando", "addr", addr)

	// ListenAndServe bloquea hasta que falle. Devuelve un error, no lanza
	// excepciones: en Go los errores son valores que tú revisas.
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("el servidor se detuvo", "error", err)
		os.Exit(1)
	}
}
