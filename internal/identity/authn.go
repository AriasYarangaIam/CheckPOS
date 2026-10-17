package identity

import (
	"context"
	"net/http"
	"strings"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// Este archivo implementa el mecanismo de RUTA AUTENTICADA del contrato: lee el access token de
// `Authorization: Bearer <token>`, lo verifica con el emisor del proceso, y deja los claims en
// el contexto de la petición para que el handler los consuma.
//
// Es el ÚNICO camino por el que un handler se entera de su tenant (ARCHITECTURE.md:23, :124):
// el tenant sale del token o la petición no está autenticada. Este middleware no lee un tenant
// del cuerpo, de la consulta ni de ninguna otra cabecera, y por eso un campo de tenant que un
// cliente mande de más no se lee ni se rechaza: no existe (docs/BACKEND.md §5.1).
//
// Ausencia, esquema distinto, token ilegible, token vencido o firma de otra clave son la MISMA
// respuesta: 401 con el sobre de error único (§4.1). El mensaje no distingue la causa, para no
// describirle al cliente la forma de lo que le falta.

const (
	// authorizationHeader es la única cabecera de credencial del contrato.
	authorizationHeader = "Authorization"

	// bearerScheme es el esquema de RFC 7235. Se compara sin distinguir mayúsculas.
	bearerScheme = "Bearer"
)

// invalidTokenMessage es el mensaje ÚNICO del 401 de las rutas autenticadas. No dice si falta el
// token, si venció o si la firma no corresponde.
const invalidTokenMessage = "the access token is missing or invalid"

// claimsContextKey es la clave del contexto. Es un tipo propio y vacío para que ninguna otra
// parte del proceso pueda escribir un valor con la misma clave por accidente.
type claimsContextKey struct{}

// ClaimsFromContext devuelve los claims que dejó el middleware de token.
//
// El segundo valor es la parte importante: `false` significa que la petición NO pasó por el
// middleware, es decir que la ruta se montó sin autenticación. Un handler tiene que tratar esa
// ausencia como el defecto de programación que es y no como un tenant vacío: los claims en cero
// llevarían a escribir con un tenant nulo, y el helper de transacción —que rechaza el UUID
// nulo— sería lo único que lo detendría.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(Claims)
	return claims, ok
}

// RequireAccessToken envuelve un handler y le exige un access token verificado.
//
// El montaje en cmd/api es el del contrato:
//
//	mux.Handle("POST /auth/device-bind", identity.RequireAccessToken(issuer, handler))
//
// Un emisor o un handler nulo es un error de cableado, no una petición: se detiene al armar el
// router —antes de aceptar conexiones— porque un middleware sin emisor no podría verificar nada
// y dejaría pasar cualquier cosa.
func RequireAccessToken(issuer *TokenIssuer, next http.Handler) http.Handler {
	if issuer == nil {
		panic("identity: RequireAccessToken needs a token issuer")
	}
	if next == nil {
		panic("identity: RequireAccessToken needs a handler to protect")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get(authorizationHeader))
		if !ok {
			httpx.WriteError(w, httpx.Unauthenticated(invalidTokenMessage))
			return
		}

		// La verificación devuelve los tres claims o un error, nunca un token a medias: un
		// tenant sin usuario (o al revés) no puede llegar al contexto (§5.1).
		claims, err := issuer.Verify(token)
		if err != nil {
			// El token no se registra: es una credencial portadora mientras vive (§7.9).
			httpx.WriteError(w, httpx.Unauthenticated(invalidTokenMessage))
			return
		}

		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
	})
}

// withClaims deja los claims verificados en el contexto de la petición.
func withClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// bearerToken extrae el token de la cabecera de credencial. Un esquema distinto de `Bearer`, una
// cabecera ausente o un token vacío devuelven false: son la misma cosa para el llamador.
func bearerToken(header string) (string, bool) {
	scheme, rest, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, bearerScheme) {
		return "", false
	}

	token := strings.TrimSpace(rest)
	if token == "" {
		return "", false
	}
	return token, true
}
