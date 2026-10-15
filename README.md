# mini-paas

Mini plataforma tipo Heroku/Vercel escrita en Go con la librería estándar.
Proyecto de aprendizaje: registras una app con la URL de un repositorio y la
plataforma la clona, la construye con Docker y la publica detrás de Caddy.

## Estado
- [x] Servidor HTTP y `GET /healthz`
- [x] PostgreSQL y modelo de datos
- [x] API `POST/GET/DELETE /apps`
- [ ] Wrapper de Docker
- [ ] Deploy orquestado
- [ ] Integración con Caddy

## Requisitos
Go 1.22+ y Docker.

## Cómo correrlo
    docker compose up -d
    docker compose exec -T postgres psql -U paas -d paas < migrations/001_init.sql
    go run ./cmd/paas

## Probar
    curl localhost:8080/healthz
    curl -X POST localhost:8080/apps -d '{"name":"hola","repo_url":"https://github.com/x/y"}'
    curl localhost:8080/apps

## Nota
Las credenciales de Postgres (`paas/paas`) son solo para desarrollo local.
