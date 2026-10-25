// Package proxy habla con la API de administración de Caddy para registrar y
// quitar las rutas <app>.<dominio> -> localhost:<puerto>.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Estos structs reflejan el JSON que espera Caddy. Los tags json:"..." le dicen
// a encoding/json cómo llamar a cada campo; "@id" es un nombre especial de
// Caddy para poder encontrar una ruta después sin recorrer toda la config.
type route struct {
	ID     string    `json:"@id"`
	Match  []match   `json:"match"`
	Handle []handler `json:"handle"`
}

type match struct {
	Host []string `json:"host"`
}

type handler struct {
	Handler   string     `json:"handler"`
	Upstreams []upstream `json:"upstreams"`
}

type upstream struct {
	Dial string `json:"dial"`
}

// StatusError es un error propio: Caddy respondió, pero con un código no 2xx.
// Al ser un tipo (y no solo un texto), quien llama puede inspeccionarlo con
// errors.As y decidir según el código, igual que hicimos con *pgconn.PgError.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("caddy respondió %d: %s", e.Code, e.Body)
}

type Client struct {
	baseURL string
	http    *http.Client
	// Caddy da 400 si dos peticiones intentan crear a la vez la misma ruta
	// (mismo @id). El mutex hace que SetRoute y RemoveRoute se turnen.
	mu sync.Mutex
}

// NewClient crea un cliente con timeout. El http.Client por defecto de Go NO
// tiene timeout: si Caddy se colgara, nuestra llamada esperaría para siempre.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// SetRoute hace que host apunte a localhost:port. Sirve tanto para crear la
// ruta como para reemplazarla en un redeploy: borra la anterior (si existe) y
// añade la nueva, así nunca queda duplicada.
func (c *Client) SetRoute(ctx context.Context, host string, port int) error {
	host = strings.ToLower(host) // el match de Caddy ignora mayúsculas, el @id no
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.remove(ctx, host); err != nil {
		return err
	}
	r := route{
		ID:    routeID(host),
		Match: []match{{Host: []string{host}}},
		Handle: []handler{{
			Handler:   "reverse_proxy",
			Upstreams: []upstream{{Dial: fmt.Sprintf("localhost:%d", port)}},
		}},
	}
	if err := c.do(ctx, http.MethodPost, "/config/apps/http/servers/srv0/routes", r); err != nil {
		return fmt.Errorf("añadir ruta %s: %w", host, err)
	}
	return nil
}

// RemoveRoute quita la ruta de host. Si no existía, no es un error.
func (c *Client) RemoveRoute(ctx context.Context, host string) error {
	host = strings.ToLower(host)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remove(ctx, host)
}

// remove asume que el mutex ya está tomado por quien llama.
func (c *Client) remove(ctx context.Context, host string) error {
	path := "/id/" + url.PathEscape(routeID(host))
	err := c.do(ctx, http.MethodDelete, path, nil)

	// errors.As recorre la cadena de errores buscando un *StatusError.
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusNotFound {
		return nil // no existía: ya está como queríamos
	}
	if err != nil {
		return fmt.Errorf("quitar ruta %s: %w", host, err)
	}
	return nil
}

func routeID(host string) string { return "route-" + host }

// do envía una petición a Caddy. Si body no es nil, lo manda como JSON.
func (c *Client) do(ctx context.Context, method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("codificar JSON: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("crear petición: %w", err)
	}
	if body != nil {
		// Sin este header Caddy responde 400.
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	// Siempre hay que cerrar el cuerpo de la respuesta, o se filtran conexiones.
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// LimitReader evita leer una respuesta gigante en memoria.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	io.Copy(io.Discard, resp.Body) // vaciar el cuerpo permite reutilizar la conexión
	return nil
}
