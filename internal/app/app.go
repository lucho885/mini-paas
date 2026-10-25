// Package app contiene el modelo de dominio: qué es una App y qué es un
// Deployment. No sabe nada de HTTP ni de SQL; solo define los datos.
package app

import "time"

// App es un struct: un conjunto de campos con tipo, como una interface de
// TypeScript, pero en Go también ES el valor real (no se borra al compilar).
//
// Los nombres con mayúscula inicial (ID, Name...) son EXPORTADOS: visibles
// desde otros paquetes. Con minúscula serían privados al paquete. En Go la
// visibilidad no usa public/private, la decide la primera letra.
//
// Los textos entre `backticks` son "tags": metadatos que lee encoding/json
// para saber cómo llamar a cada campo en el JSON ("repo_url" en vez de RepoURL).
type App struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	RepoURL   string    `json:"repo_url"`
	CreatedAt time.Time `json:"created_at"`
}

// Deployment es un despliegue concreto de una App.
//
// ContainerID y HostPort son *string y *int: punteros. Un puntero puede ser
// nil, y así representamos "todavía no tiene valor" (NULL en la base de datos).
// En TypeScript usarías `string | null`; en Go un string normal nunca es nil,
// su valor vacío es "".
type Deployment struct {
	ID          int64     `json:"id"`
	AppID       int64     `json:"app_id"`
	Status      string    `json:"status"`
	ContainerID *string   `json:"container_id"`
	HostPort    *int      `json:"host_port"`
	Error       *string   `json:"error"` // motivo del fallo; nil si no ha fallado
	CreatedAt   time.Time `json:"created_at"`
}
