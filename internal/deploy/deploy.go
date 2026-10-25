// Package deploy orquesta un despliegue completo: clonar el repo, construir la
// imagen, arrancar el contenedor y guardar el estado en la base de datos.
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mini-paas/internal/app"
	"mini-paas/internal/docker"
	"mini-paas/internal/proxy"
)

const (
	// Por ahora toda app debe escuchar en este puerto dentro de su contenedor.
	containerPort = 8080
	// Tiempo máximo que puede tardar un despliegue completo.
	deployTimeout = 10 * time.Minute
)

// validName: el nombre acaba en tags de imagen y nombres de contenedor, que
// solo admiten minúsculas, números y guiones.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type Deployer struct {
	apps    *app.Repository
	docker  *docker.Client
	proxy   *proxy.Client
	domain  string // dominio base: la app "hola" se publica en hola.<domain>
	workDir string // dónde se clonan los repos; "" usa el directorio temporal del sistema
	logger  *slog.Logger
}

func NewDeployer(apps *app.Repository, docker *docker.Client, caddy *proxy.Client, domain, workDir string, logger *slog.Logger) *Deployer {
	return &Deployer{apps: apps, docker: docker, proxy: caddy, domain: domain, workDir: workDir, logger: logger}
}

// Start lanza el despliegue en segundo plano y vuelve al instante.
//
// `go f()` arranca f en una goroutine: una tarea concurrente muy barata,
// gestionada por el runtime de Go (no es un hilo del sistema). Es lo más
// parecido a llamar una función async sin hacer await.
//
// Usamos context.Background() y no el contexto de la petición HTTP: ese se
// cancela en cuanto respondemos al cliente, y mataría el build a medias.
func (d *Deployer) Start(a app.App, dep app.Deployment) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deployTimeout)
		defer cancel()

		log := d.logger.With("app", a.Name, "deployment", dep.ID)
		if err := d.deploy(ctx, a, dep); err != nil {
			log.Error("deploy fallido", "error", err)

			// ctx puede haber vencido (timeout), así que para guardar el fallo
			// usamos un contexto nuevo y corto.
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer saveCancel()
			if err := d.apps.MarkFailed(saveCtx, dep.ID, err.Error()); err != nil {
				log.Error("guardar estado failed", "error", err)
			}
			return
		}
		log.Info("deploy completado")
	}()
}

// deploy hace el trabajo real, en orden, y devuelve el primer error que ocurra.
func (d *Deployer) deploy(ctx context.Context, a app.App, dep app.Deployment) error {
	if !validName.MatchString(a.Name) {
		return fmt.Errorf("nombre de app inválido %q: usa minúsculas, números y guiones", a.Name)
	}
	if !strings.HasPrefix(a.RepoURL, "https://") {
		return fmt.Errorf("repo_url debe empezar por https://")
	}

	if err := d.apps.UpdateStatus(ctx, dep.ID, app.StatusBuilding); err != nil {
		return err
	}

	// Directorio temporal para el clon; se borra al terminar pase lo que pase.
	dir, err := os.MkdirTemp(d.workDir, a.Name+"-")
	if err != nil {
		return fmt.Errorf("crear directorio: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := gitClone(ctx, a.RepoURL, dir); err != nil {
		return err
	}

	tag := fmt.Sprintf("paas-%s:%d", a.Name, dep.ID)
	if err := d.docker.Build(ctx, dir, tag); err != nil {
		return err
	}

	hostPort, err := freePort()
	if err != nil {
		return err
	}

	// Apuntamos el despliegue anterior (si lo hay) antes de arrancar el nuevo.
	previous, err := d.apps.LatestRunning(ctx, a.ID)
	if err != nil && !errors.Is(err, app.ErrNotFound) {
		return err
	}

	name := fmt.Sprintf("paas-%s-%d", a.Name, dep.ID)
	// Variables configuradas para la app, más PORT, que fija la plataforma para
	// que la app sepa en qué puerto escuchar. Se leen aquí, justo antes de
	// arrancar, para que cuenten los últimos cambios.
	env, err := d.apps.GetEnv(ctx, a.ID)
	if err != nil {
		return err
	}
	env["PORT"] = strconv.Itoa(containerPort)

	containerID, err := d.docker.Run(ctx, tag, name, hostPort, containerPort, env)
	if err != nil {
		return err
	}

	// Publicamos la URL estable <app>.<dominio> apuntando al puerto nuevo. Si
	// ya existía (redeploy), SetRoute la reemplaza. Sin ruta la app no se
	// alcanzaría por su URL, así que si falla paramos el contenedor nuevo.
	host := a.Name + "." + d.domain
	if err := d.proxy.SetRoute(ctx, host, hostPort); err != nil {
		if stopErr := d.docker.Stop(ctx, containerID); stopErr != nil {
			d.logger.Warn("parar contenedor sin ruta", "container", containerID, "error", stopErr)
		}
		return fmt.Errorf("registrar ruta: %w", err)
	}

	if err := d.apps.MarkRunning(ctx, dep.ID, containerID, hostPort); err != nil {
		return err
	}

	// El nuevo ya está arriba: retiramos el anterior. Un fallo aquí no invalida
	// el despliegue nuevo, así que solo lo registramos.
	if previous.ContainerID != nil {
		if err := d.docker.Stop(ctx, *previous.ContainerID); err != nil {
			d.logger.Warn("parar despliegue anterior", "deployment", previous.ID, "error", err)
		}
		if err := d.apps.UpdateStatus(ctx, previous.ID, app.StatusStopped); err != nil {
			d.logger.Warn("marcar anterior como stopped", "deployment", previous.ID, "error", err)
		}
	}
	return nil
}

// Reconcile alinea la realidad con la base de datos. La base de datos es la
// fuente de verdad de qué debería estar publicado, pero Caddy guarda sus rutas
// en memoria (se pierden si se reinicia) y los contenedores pueden haber muerto
// (reinicio del equipo). Para cada deployment marcado running:
//   - si su contenedor sigue vivo, vuelve a registrar su ruta en Caddy;
//   - si no, lo marca stopped para que la base de datos diga la verdad.
//
// Es seguro ejecutarlo varias veces: SetRoute es idempotente.
func (d *Deployer) Reconcile(ctx context.Context) {
	running, err := d.apps.ListRunning(ctx)
	if err != nil {
		d.logger.Error("reconciliar: listar deployments", "error", err)
		return
	}

	restored, stopped := 0, 0
	for _, r := range running {
		log := d.logger.With("app", r.AppName, "deployment", r.ID)
		if r.ContainerID == nil || r.HostPort == nil {
			continue // no debería pasar: running siempre guarda ambos
		}

		alive, err := d.docker.IsRunning(ctx, *r.ContainerID)
		if err != nil {
			log.Warn("reconciliar: consultar contenedor", "error", err)
			continue // no sabemos su estado: mejor no tocar nada
		}
		if !alive {
			if err := d.apps.UpdateStatus(ctx, r.ID, app.StatusStopped); err != nil {
				log.Warn("reconciliar: marcar stopped", "error", err)
				continue
			}
			stopped++
			continue
		}

		if err := d.proxy.SetRoute(ctx, r.AppName+"."+d.domain, *r.HostPort); err != nil {
			log.Warn("reconciliar: registrar ruta", "error", err)
			continue
		}
		restored++
	}
	d.logger.Info("reconciliación terminada", "rutas_restauradas", restored, "marcados_stopped", stopped)
}

// Logs devuelve las últimas tail líneas del contenedor activo de la app.
// Devuelve app.ErrNotFound si la app no tiene ningún contenedor en ejecución.
func (d *Deployer) Logs(ctx context.Context, appID int64, tail int) (string, error) {
	running, err := d.apps.LatestRunning(ctx, appID)
	if err != nil {
		return "", err // incluye app.ErrNotFound, que el handler traduce a 404
	}
	if running.ContainerID == nil {
		return "", app.ErrNotFound
	}
	return d.docker.Logs(ctx, *running.ContainerID, tail)
}

// Teardown deja de publicar la app: quita su ruta de Caddy y para el contenedor
// activo. Se llama antes de borrar la app de la base de datos.
//
// Los errores se registran pero no se devuelven: que Caddy o Docker estén
// caídos no debe impedir que el usuario borre su app. El orden importa: primero
// la ruta, luego el contenedor, para no servir un 502 entre un paso y otro.
func (d *Deployer) Teardown(ctx context.Context, a app.App) {
	log := d.logger.With("app", a.Name)

	if err := d.proxy.RemoveRoute(ctx, a.Name+"."+d.domain); err != nil {
		log.Warn("quitar ruta", "error", err)
	}

	running, err := d.apps.LatestRunning(ctx, a.ID)
	if errors.Is(err, app.ErrNotFound) {
		return // nunca se desplegó o ya estaba parada
	}
	if err != nil {
		log.Warn("buscar deployment activo", "error", err)
		return
	}
	if running.ContainerID != nil {
		if err := d.docker.Stop(ctx, *running.ContainerID); err != nil {
			log.Warn("parar contenedor", "container", *running.ContainerID, "error", err)
		}
	}
}

// gitClone clona solo el último commit (--depth 1), que es más rápido.
func gitClone(ctx context.Context, repoURL, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", repoURL, dir)
	// Sin esto, git se quedaría esperando usuario/contraseña en un repo privado.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// freePort pide al sistema operativo un puerto libre: escuchar en ":0" hace que
// el SO elija uno. Lo cerramos enseguida para que docker pueda usarlo. Existe
// una pequeña ventana en la que otro proceso podría tomarlo, aceptable aquí.
func freePort() (int, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("buscar puerto libre: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
