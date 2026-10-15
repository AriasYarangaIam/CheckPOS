// Package httpx concentra el transporte HTTP compartido: el sobre de error único del
// contrato (docs/BACKEND.md §4.1), los ayudantes de JSON y el registro de peticiones.
//
// El sobre es exactamente uno y no se negocia:
//
//	{ "error": { "code": "VALIDATION_ERROR", "message": "human-readable", "details": { } } }
//
// `code` es legible por máquina y estable; `message` es legible por humano; `details`
// es opcional. Ningún camino de este paquete responde `null` ni texto plano como error,
// y ninguna respuesta mezcla estrategias.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Códigos de error del contrato. Los cinco primeros corresponden a los códigos de
// estado de negocio de docs/BACKEND.md §4.1; CodeNotFound cubre el recurso inexistente o
// fuera del tenant; CodeNotReady es el código operativo de GET /readyz, un endpoint de
// operación fuera de la superficie de negocio (§7.9).
const (
	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeNotFound        = "NOT_FOUND"
	CodeConflict        = "CONFLICT"
	CodeValidationError = "VALIDATION_ERROR"
	CodeInternalError   = "INTERNAL_ERROR"
	CodeNotReady        = "NOT_READY"
)

// Error es un error del contrato: lleva el estado HTTP al que corresponde, el código
// estable y el mensaje humano del sobre, más un campo `details` opcional.
//
// Implementa error, así que un handler puede devolverlo hacia arriba y el transporte lo
// escribe con WriteError sin volver a decidir el estado ni la forma.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

// Error cumple la interfaz error con el código y el mensaje, sin el detalle opcional.
func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// errorBody es el objeto interno del sobre. El orden de los campos es el del contrato y
// `details` se omite cuando no lleva nada: nunca se envía `null`.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// errorEnvelope es la única clave de nivel superior de toda respuesta de error.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// Unauthenticated construye el 401 del contrato (no autenticado).
func Unauthenticated(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: CodeUnauthenticated, Message: message}
}

// NotFound construye el 404 del contrato: el recurso no existe o está fuera del tenant,
// porque el RLS devolvió cero filas y la respuesta no debe distinguir los dos casos.
func NotFound(message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: message}
}

// Conflict construye el 409 del contrato: misma clave de idempotencia con una petición
// todavía en vuelo.
func Conflict(message string) *Error {
	return &Error{Status: http.StatusConflict, Code: CodeConflict, Message: message}
}

// Validation construye el 422 del contrato: cuerpo inválido, o misma clave con un cuerpo
// distinto. `details` es opcional y suele llevar los campos que fallaron.
func Validation(message string, details any) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodeValidationError, Message: message, Details: details}
}

// NotReady construye el 503 del sondeo de preparación. Es un código operativo: no forma
// parte de la superficie de negocio de §4.
//
// El mensaje es genérico a propósito. El error del driver lleva host, usuario y puerto, y
// este endpoint lo consulta cualquiera que pueda alcanzar el proceso.
func NotReady() *Error {
	return &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    CodeNotReady,
		Message: "a required dependency is not reachable",
	}
}

// Internal envuelve una causa inesperada: registra el detalle para el operador y
// devuelve un sobre genérico. El cliente nunca ve el error interno: un 500 "nunca expone
// detalles internos" (§4.1).
func Internal(cause error, logger *slog.Logger) *Error {
	if logger != nil {
		logger.Error("internal error", "error", cause)
	}
	return &Error{
		Status:  http.StatusInternalServerError,
		Code:    CodeInternalError,
		Message: "the server could not complete the request",
	}
}

// WriteJSON escribe una respuesta con su estado y su tipo de contenido.
//
// Es el único lugar del paquete que fija la cabecera, para que ninguna respuesta la
// olvide ni la escriba después del cuerpo.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError escribe el sobre de error único, con el estado que el propio error declara.
func WriteError(w http.ResponseWriter, err *Error) {
	status := err.Status
	if status == 0 {
		// Un Error construido a mano sin estado no puede terminar siendo un 200.
		status = http.StatusInternalServerError
	}

	WriteJSON(w, status, errorEnvelope{
		Error: errorBody{Code: err.Code, Message: err.Message, Details: err.Details},
	})
}
