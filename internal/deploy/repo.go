package deploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Errores centinela: quien llama los distingue con errors.Is para elegir el
// código HTTP adecuado.
var (
	ErrInvalidRepoURL  = errors.New("repo_url debe empezar por https://")
	ErrRepoUnreachable = errors.New("no se pudo acceder al repositorio")
)

const checkRepoTimeout = 15 * time.Second

// CheckRepo comprueba que el repositorio existe y se puede leer sin credenciales.
//
// `git ls-remote` pregunta al servidor por las referencias del repo sin
// descargarlo, así que es rápido y barato. Si la URL no existe, el repo es
// privado o está vacío, falla.
func CheckRepo(ctx context.Context, repoURL string) error {
	// Solo https://. Además de ser la regla de la plataforma, evita que una
	// "URL" que empiece por "-" git la interprete como una opción.
	if !strings.HasPrefix(repoURL, "https://") {
		return ErrInvalidRepoURL
	}

	ctx, cancel := context.WithTimeout(ctx, checkRepoTimeout)
	defer cancel()

	// "--" marca el fin de las opciones; lo que sigue son datos, nunca flags.
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", "--", repoURL, "HEAD")
	// Sin esto, git esperaría usuario y contraseña en un repo privado.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if err := cmd.Run(); err != nil {
		// Si venció el plazo, el proceso murió por nuestra culpa: no es que el
		// repo no exista, y el handler lo distingue de este otro caso.
		if ctx.Err() != nil {
			return fmt.Errorf("comprobar repositorio: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return ErrRepoUnreachable // git respondió con error: no existe, es privado o está vacío
		}
		return fmt.Errorf("ejecutar git: %w", err) // git no está instalado, etc.
	}
	return nil
}
