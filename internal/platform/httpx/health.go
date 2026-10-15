package httpx

import (
	"context"
	"net/http"
)

// statusOK es el único cuerpo de éxito de los sondeos de operación.
const statusOK = "ok"

// statusBody es la respuesta de éxito de los sondeos. No pertenece a la superficie de
// negocio de docs/BACKEND.md §4: es la forma mínima que un balanceador o un orquestador
// necesita leer.
type statusBody struct {
	Status string `json:"status"`
}

// ReadinessChecker es todo lo que un sondeo de preparación necesita: algo que responda
// si sus dependencias están alcanzables ahora mismo.
//
// *pgxpool.Pool satisface esta interfaz tal cual, así que cmd/api entrega el pool sin
// ningún adaptador y las pruebas entregan un doble.
type ReadinessChecker interface {
	Ping(ctx context.Context) error
}

// HealthHandler es el sondeo de vida: responde 200 sin consultar ninguna dependencia.
//
// Es deliberado. Si PostgreSQL se cae, el proceso sigue vivo y debe quedarse vivo: un
// orquestador que reinicia por un fallo de base solo agrega una caída a la caída. Lo que
// deja de estar disponible se reporta en /readyz.
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, statusBody{Status: statusOK})
	}
}

// ReadinessHandler es el sondeo de preparación: 200 si las dependencias responden y 503
// con el sobre de error si no.
//
// El error del driver no se devuelve (ver NotReady): se registra, y el cliente recibe un
// mensaje genérico.
func ReadinessHandler(checker ReadinessChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if checker == nil || checker.Ping(r.Context()) != nil {
			WriteError(w, NotReady())
			return
		}
		WriteJSON(w, http.StatusOK, statusBody{Status: statusOK})
	}
}

// NotFoundHandler responde una ruta desconocida con el sobre de error, para que ningún
// cliente reciba el "404 page not found" en texto plano de net/http y el contrato de §4.1
// siga siendo cierto también fuera de la superficie de negocio.
func NotFoundHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, NotFound("the requested route does not exist"))
	}
}
