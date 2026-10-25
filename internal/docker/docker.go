package docker

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Client no guarda estado: solo agrupa los métodos que llaman a la CLI de docker.
type Client struct{}

func NewClient() *Client { return &Client{} }

// Build construye una imagen a partir del Dockerfile de contextDir.
func (c *Client) Build(ctx context.Context, contextDir, tag string) error {
	_, err := c.run(ctx, "build", "-t", tag, contextDir)
	return err
}

// Run arranca un contenedor en segundo plano y devuelve su ID. env son las
// variables de entorno que recibirá la app (puede ser nil).
func (c *Client) Run(ctx context.Context, tag, name string, hostPort, containerPort int, env map[string]string) (string, error) {
	// --restart unless-stopped: Docker lo vuelve a arrancar tras un reinicio del
	// equipo o del daemon, salvo que lo hayamos parado nosotros con Stop.
	args := []string{"run", "-d", "--name", name,
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("%d:%d", hostPort, containerPort)}

	// Las variables van en un archivo temporal (--env-file) y no como "-e K=V":
	// los argumentos de un comando son visibles para cualquiera con `ps`, y
	// aquí pueden viajar secretos.
	if len(env) > 0 {
		path, err := writeEnvFile(env)
		if err != nil {
			return "", err
		}
		defer os.Remove(path) // docker ya leyó el archivo cuando `run` termina
		args = append(args, "--env-file", path)
	}
	args = append(args, tag)

	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil // docker imprime el ID del contenedor
}

// writeEnvFile guarda las variables en un archivo temporal con formato
// CLAVE=valor, una por línea, y devuelve su ruta. os.CreateTemp lo crea con
// permisos 0600: solo el usuario que corre el servidor puede leerlo.
func writeEnvFile(env map[string]string) (string, error) {
	f, err := os.CreateTemp("", "paas-env-*")
	if err != nil {
		return "", fmt.Errorf("crear archivo de variables: %w", err)
	}
	// Los maps de Go se recorren en orden aleatorio; ordenamos las claves para
	// que el archivo sea siempre igual (más fácil de depurar).
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, env[k]); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", fmt.Errorf("escribir archivo de variables: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("cerrar archivo de variables: %w", err)
	}
	return f.Name(), nil
}

// Stop detiene y elimina el contenedor.
func (c *Client) Stop(ctx context.Context, containerID string) error {
	if _, err := c.run(ctx, "stop", containerID); err != nil {
		return err
	}
	_, err := c.run(ctx, "rm", containerID)
	return err
}

// Logs devuelve las últimas tail líneas del contenedor, con lo que la app
// escribió por stdout y por stderr.
//
// docker logs reparte la salida de la app en los dos streams, y muchas apps
// (y casi todos los errores) escriben por stderr. Con c.run solo veríamos
// stdout. Aquí ambos apuntan al MISMO buffer: os/exec entonces usa un único
// pipe y las líneas conservan su orden original.
func (c *Client) Logs(ctx context.Context, containerID string, tail int) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(tail), containerID)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker logs: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// IsRunning dice si el contenedor existe y está en ejecución. Si Docker no lo
// conoce (borrado a mano, por ejemplo) devuelve false sin error; cualquier otro
// fallo (daemon caído...) sí se devuelve, para no confundirlo con "parado".
func (c *Client) IsRunning(ctx context.Context, containerID string) (bool, error) {
	out, err := c.run(ctx, "inspect", "-f", "{{.State.Running}}", containerID)
	if err != nil {
		if strings.Contains(err.Error(), "No such") {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// run ejecuta `docker <args...>` y devuelve stdout.
// Si falla, envuelve el error con %w e incluye stderr para poder diagnosticar.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
