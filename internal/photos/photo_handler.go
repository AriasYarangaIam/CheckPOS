package photos

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// Este archivo implementa la superficie HTTP del upload de fotos (W3-BE-3, docs/BACKEND.md
// §10.3): POST /photos es multipart con el campo photo, autenticada, y responde 200 con la
// URL pública del objeto —SOLO la URL—.
//
// El contrato de la superficie es corto y sus reglas son rígidas:
//
//   - el tenant es el del token: la foto queda en el espacio de objetos del tenant que la
//     sube y de NINGÚN otro lugar;
//   - la respuesta es {"url": ...} y la base de datos queda OUT del camino: la URL es la
//     única pieza que el cliente mandará después en photo_url dentro del cuerpo de
//     POST /products (W3-BE-1 ya la acepta como campo opcional);
//   - el payload tiene tope (MaxPhotoBytes, propuesta documentada del ticket) y un cuerpo
//     que no es multipart, o un tipo fuera de la lista cerrada, es 422 con el campo
//     nombrado: rechazos de CUERPO, no fallos del store.

// MaxPhotoBytes es el tope por foto. Las fuentes no lo fijan, así que la elección queda
// documentada (propuesta): 5 MiB cubre la foto de un producto en una cámara de mostrador
// con margen, y acota el peor caso de memoria del handler.
const MaxPhotoBytes = 5 << 20

// errNoPhotoClaims es la causa que se registra cuando la ruta corre sin el middleware de
// token: es un defecto de cableado, no una petición anónima.
var errNoPhotoClaims = errors.New("the route ran without the access token middleware: the photo upload needs a tenant")

// photosHandler atiende POST /photos sobre un store de objetos.
type PhotosHandler struct {
	store  Store
	logger *slog.Logger
}

// NewPhotosHandler construye el handler sobre el store de objetos.
func NewPhotosHandler(store Store, logger *slog.Logger) (*PhotosHandler, error) {
	if store == nil {
		return nil, errors.New("the photos handler needs an object store")
	}
	if logger == nil {
		return nil, errors.New("the photos handler needs a logger")
	}
	return &PhotosHandler{store: store, logger: logger}, nil
}

// photoUploadResponse es la respuesta del upload: la URL pública del objeto, NADA más.
type photoUploadResponse struct {
	URL string `json:"url"`
}

// ServeHTTP resuelve la única forma de la ruta.
func (h *PhotosHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// El tenant sale del token, como en toda ruta de negocio; el middleware de token es la
	// única fuente (ARCHITECTURE.md:23).
	claims, ok := identity.ClaimsFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.Internal(errNoPhotoClaims, h.logger))
		return
	}
	if r.Method != http.MethodPost {
		httpx.WriteError(w, httpx.NotFound("this route only answers POST"))
		return
	}

	h.upload(w, r, claims.TenantID)
}

// upload guarda la foto y responde exclusivamente con la URL pública.
func (h *PhotosHandler) upload(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID) {
	// El tope del CUERPO entero: el cap de MaxBytesReader acota el peor caso de memoria del
	// handler y convierte un cuerpo gigante en el mismo 422 familiar de un cuerpo ilegal,
	// sin que un adversario pueda invertir el módulo con 2 GiB de multipart.
	r.Body = http.MaxBytesReader(w, r.Body, MaxPhotoBytes)

	if err := r.ParseMultipartForm(MaxPhotoBytes); err != nil {
		httpx.WriteError(w, httpx.Validation(
			"the photo must travel as the multipart field photo",
			map[string]string{"field": "photo"}))
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		httpx.WriteError(w, httpx.Validation(
			"the photo must travel as the multipart field photo",
			map[string]string{"field": "photo"}))
		return
	}
	defer file.Close()

	// La lista de tipos es cerrada y se verifica ANTES del store: es un cuerpo ilegal, no
	// un fallo de la caja fuerte.
	contentType := header.Header.Get("Content-Type")
	if _, ok := Extension(contentType); !ok {
		httpx.WriteError(w, httpx.Validation(
			"the photo must be image/jpeg, image/png or image/webp",
			map[string]string{"field": "photo"}))
		return
	}

	photo, err := io.ReadAll(file)
	if err != nil {
		httpx.WriteError(w, httpx.Validation(
			"the photo could not be read",
			map[string]string{"field": "photo"}))
		return
	}
	if len(photo) > MaxPhotoBytes {
		httpx.WriteError(w, httpx.Validation(
			"the photo is larger than the 5 MiB limit",
			map[string]string{"field": "photo"}))
		return
	}

	url, err := h.store.Put(r.Context(), tenantID, photo, contentType)
	if err != nil {
		httpx.WriteError(w, httpx.Internal(err, h.logger))
		return
	}

	h.logger.Info("photo uploaded", "tenant_id", tenantID.String())
	httpx.WriteJSON(w, http.StatusOK, photoUploadResponse{URL: url})
}
