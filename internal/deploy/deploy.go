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
	"strings"
	"time"

	"mini-paas/internal/app"
	"mini-paas/internal/docker"
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
	workDir string // dónde se clonan los repos; "" usa el directorio temporal del sistema
	logger  *slog.Logger
}

func NewDeployer(apps *app.Repository, docker *docker.Client, workDir string, logger *slog.Logger) *Deployer {
	return &Deployer{apps: apps, docker: docker, workDir: workDir, logger: logger}
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
			if err := d.apps.UpdateStatus(saveCtx, dep.ID, app.StatusFailed); err != nil {
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
	containerID, err := d.docker.Run(ctx, tag, name, hostPort, containerPort)
	if err != nil {
		return err
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
