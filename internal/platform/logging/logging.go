// Package logging construye el registro estructurado del proceso y transporta el
// identificador de petición por el contexto.
//
// El formato es un objeto JSON por línea, con el nivel, el mensaje y los campos que
// acompañan a cada evento (docs/BACKEND.md §7.9). Una línea por evento es lo que hace
// que el registro se pueda filtrar y agregar sin expresiones regulares.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"strconv"
	"time"
)

// requestIDKey es el tipo de la clave del contexto. Es un tipo propio y no una cadena
// para que ningún otro paquete pueda pisar el identificador por accidente.
type requestIDKey struct{}

// New devuelve el logger del proceso, escribiendo en w con el nivel pedido.
//
// Un nivel desconocido cae a info en lugar de fallar: config.Load ya lo validó, así que
// llegar aquí con un valor inválido es un defecto de programación, y un logger que no
// arranca es peor que un logger demasiado verboso.
func New(level string, w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)}))
}

// parseLevel traduce el nivel de configuración al de slog.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewRequestID genera un identificador de petición: 16 bytes aleatorios en hexadecimal.
// Se genera en el transporte y viaja a los logs para poder correlacionar todo lo que
// provocó una petición (§7.9).
func NewRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand no falla en la práctica. Si fallara, un identificador basado en
		// el reloj sigue sirviendo para correlacionar, y es preferible a uno vacío.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}

// WithRequestID guarda el identificador de petición en el contexto.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID devuelve el identificador de petición del contexto, o la cadena vacía
// cuando no hay ninguno.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
