# mini-paas

Mini plataforma tipo Heroku/Vercel escrita en Go con la librería estándar.
Proyecto de aprendizaje: registras una app con la URL de un repositorio y la
plataforma la clona, la construye con Docker y la publica detrás de Caddy en
`<nombre>.localhost`.

## Estado
- [x] Servidor HTTP y `GET /healthz`
- [x] PostgreSQL y modelo de datos
- [x] API `POST/GET/DELETE /apps`
- [x] Wrapper de Docker (`internal/docker`)
- [x] Deploy orquestado en segundo plano (`internal/deploy`)
- [x] Integración con Caddy (`internal/proxy`): URL estable por app
- [x] Reconstruir las rutas de Caddy desde la base de datos al arrancar
- [x] Variables de entorno por app, logs del contenedor y motivo de fallo del deploy
- [ ] Tests

## Cómo funciona
    curl/Insomnia ──► API Go (:8080) ──► PostgreSQL (:5432)
                          │
                          └─ deploy (goroutine): git clone → docker build → docker run
                                                  └─► registra la ruta en Caddy (:2019)
    navegador ──► Caddy (:80) ──► contenedor de la app (puerto libre del host)

Ciclo de un deploy: `pending → building → running` (o `failed`). Al redesplegar,
el contenedor anterior se para y queda `stopped`.

## Requisitos
Go 1.22+, Docker y git.

## Cómo correrlo
    docker compose up -d          # Postgres y Caddy
    docker compose exec -T postgres psql -U paas -d paas < migrations/001_init.sql
    docker compose exec -T postgres psql -U paas -d paas < migrations/002_env_and_errors.sql
    go run ./cmd/paas

## Variables de entorno
| Variable | Por defecto | Para qué |
|---|---|---|
| `DATABASE_URL` | `postgres://paas:paas@localhost:5432/paas` | Conexión a Postgres |
| `CADDY_ADMIN` | `http://localhost:2019` | API de administración de Caddy |
| `PAAS_DOMAIN` | `localhost` | Dominio base: la app `hola` se publica en `hola.<dominio>` |
| `PAAS_ADDR` | `:8080` | Dirección donde escucha la API |

## Requisitos de las apps
- El repo debe ser público, con URL `https://`.
- Debe tener un `Dockerfile` en la raíz.
- La app debe escuchar en el puerto **8080** dentro del contenedor. La plataforma
  también le pasa `PORT=8080` como variable de entorno.
- El nombre solo admite minúsculas, números y guiones (es parte del subdominio).

## Probar
    curl localhost:8080/healthz
    curl -X POST localhost:8080/apps -d '{"name":"hola","repo_url":"https://github.com/x/y"}'
    curl localhost:8080/apps
    curl -X POST localhost:8080/apps/1/deploy        # responde 202
    curl localhost:8080/apps/1/deployments           # repetir hasta ver "running"
    curl http://hola.localhost                       # la app, vía Caddy
    curl -X PUT localhost:8080/apps/1/env -d '{"GREETING":"hola"}'   # reemplaza TODAS las variables
    curl localhost:8080/apps/1/env                   # se aplican en el próximo deploy
    curl "localhost:8080/apps/1/logs?tail=100"       # últimas líneas del contenedor activo
    curl -X DELETE localhost:8080/apps/1             # para el contenedor y quita la ruta

Si un deploy queda en `failed`, el motivo está en el campo `error` de
`GET /apps/{id}/deployments`.

## Límites conocidos
- Las rutas viven en la memoria de Caddy. Al arrancar, el servidor las reconstruye
  desde la base de datos (y marca `stopped` los deployments cuyo contenedor ya no
  existe), pero si reinicias solo Caddy hay que reiniciar también el servidor.
- Si el servidor se detiene en mitad de un deploy, ese deployment queda en `building`.
- Las variables de entorno se guardan en texto plano en Postgres y `GET /apps/{id}/env`
  las devuelve con su valor: la API aún no tiene autenticación. Tampoco admiten
  saltos de línea ni la clave `PORT`.
- La API de administración de Caddy (:2019) no tiene autenticación; solo es
  accesible desde la propia máquina (`network_mode: host`).

## Nota
Las credenciales de Postgres (`paas/paas`) son solo para desarrollo local.
