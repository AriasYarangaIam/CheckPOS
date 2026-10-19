package photos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AriasYarangaIam/CheckPOS/internal/identity"
	"github.com/AriasYarangaIam/CheckPOS/internal/photos"
	"github.com/AriasYarangaIam/CheckPOS/internal/platform/httpx"
)

// La superficie del upload (docs/BACKEND.md §10.3; SPRINTS.md W3-BE-3): POST /photos es
// multipart con el campo photo, corre detrás del middleware de token, responde 200 con la
// URL pública —SOLO la URL, jamás bytes en la respuesta ni en la base— y rechaza con 422
// lo que el contrato no admite (payload sobre el tope, cuerpo que no es multipart, tipo de
// contenido no listado).
//
// El handler no toca la base de datos: la URL vuelve al cliente, y el cliente la manda
// después en photo_url dentro del cuerpo de POST /products.

// testPhotoSigningSecret es la clave de firma de las pruebas de photos: 32 bytes.
const testPhotoSigningSecret = "test-only-signing-key-0123456789abcdef"

// photoReadyStub es el sondeo de preparación del router de estas pruebas.
type photoReadyStub struct{}

func (photoReadyStub) Ping(context.Context) error { return nil }

// stubStore es el store de las pruebas del handler: cuenta los puts y devuelve el fallo
// que la prueba fija.
type stubStore struct {
	putCalls int
	err      error
}

func (s *stubStore) Put(_ context.Context, _ uuid.UUID, _ []byte, _ string) (string, error) {
	s.putCalls++
	if s.err != nil {
		return "", s.err
	}
	return "http://photos:9000/photos/019a0000-0000-7000-8000-0000000000f1/019a0000-0000-7000-8000-0000000000aa.jpg", nil
}

// newPhotoRouter arma el router REAL con POST /photos detrás del middleware de token,
// igual que cmd/api lo monta.
func newPhotoRouter(t *testing.T, store photos.Store, logBuf *strings.Builder) (http.Handler, *identity.TokenIssuer) {
	t.Helper()

	issuer, err := identity.NewTokenIssuer(testPhotoSigningSecret, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenIssuer: %v", err)
	}

	handler, err := photos.NewPhotosHandler(store, slog.New(slog.NewTextHandler(logBuf, nil)))
	if err != nil {
		t.Fatalf("NewPhotosHandler: %v", err)
	}

	router := httpx.NewRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		photoReadyStub{},
		httpx.WithRoutes(func(mux *http.ServeMux) {
			mux.Handle("POST /photos", identity.RequireAccessToken(issuer, handler))
		}),
	)
	return router, issuer
}

// doPhotoRequest envía una petición con el Content-Type pedido (multipart necesita el suyo).
func doPhotoRequest(t *testing.T, router http.Handler, method, target, token, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", contentType)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// photoEnvelope lee el sobre de error único (§4.1) de una respuesta.
type photoEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

// parsePhotoEnvelope lee el sobre de error único (§4.1) de una respuesta.
func parsePhotoEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) photoEnvelope {
	t.Helper()

	var envelope photoEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("la respuesta no es el sobre de error: %v (%q)", err, recorder.Body.String())
	}
	return envelope
}

// photoMultipart arma un cuerpo multipart con UN campo photo del tipo de contenido dado y
// devuelve el cuerpo crudo y su Content-Type completo (con el boundary).
func photoMultipart(t *testing.T, contentType string, content []byte) (string, string) {
	t.Helper()

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)

	// CreateFormFile fija el Content-Type application/octet-stream; el upload del contrato
	// SIEMPRE nombra su tipo de contenido, así que la parte se construye con encabezados
	// explícitos.
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="photo"; filename="arroz.jpg"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("escribir el archivo en el multipart: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("cerrar el multipart: %v", err)
	}

	return buffer.String(), "multipart/form-data; boundary=" + writer.Boundary()
}

// TestPhotoUploadAnswersWithAURLAndLeavesTheObjectInPlace: el upload responde 200 con la
// URL pública del objeto —que nombra al tenant del token— y el objeto quedó en el disco
// con el contenido EXACTO. Ni la respuesta ni el store llevan la foto a ningún registro de
// base: la URL es la ÚNICA cosa que se devuelve.
func TestPhotoUploadAnswersWithAURLAndLeavesTheObjectInPlace(t *testing.T) {
	dir := t.TempDir()
	store, err := photos.NewLocalStore(dir, "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	var logBuf strings.Builder
	router, issuer := newPhotoRouter(t, store, &logBuf)
	token, err := issuer.Issue(identity.Claims{
		TenantID: photoTenantID,
		UserID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000f2"),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	content := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	body, contentType := photoMultipart(t, "image/jpeg", content)
	recorder := doPhotoRequest(t, router, http.MethodPost, "/photos", token, body, contentType)
	if recorder.Code != http.StatusOK {
		t.Fatalf("estado = %d, want 200 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}

	var decoded struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (%q)", err, recorder.Body.String())
	}
	if decoded.URL == "" {
		t.Fatalf("la respuesta no trae la url: %q", recorder.Body.String())
	}
	if !strings.HasPrefix(decoded.URL, "http://photos:9000/photos/"+photoTenantID.String()+"/") {
		t.Errorf("url = %q, want el prefijo público con el tenant del TOKEN", decoded.URL)
	}
	if strings.Contains(decoded.URL, "89") {
		t.Errorf("la respuesta lleva bytes de la foto embebidos: %q", decoded.URL)
	}

	// El objeto existe en el disco con el contenido exacto (la caja fuerte del store local).
	matches, err := filepath.Glob(filepath.Join(dir, photoTenantID.String(), "*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("el tenant tiene %d objetos existentes, want 1 (%v)", len(matches), err)
	}
	stored, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("leer el objeto: %v", err)
	}
	if string(stored) != string(content) {
		t.Errorf("el objeto del disco cambió: %v, want %v", stored, content)
	}
}

// TestPhotoUploadRejectsAPayloadBiggerThanTheLimit: el tope del contrato (5 MiB, propuesta
// documentada del ticket) es 422 en la superficie y el store no corre con el objeto grande.
func TestPhotoUploadRejectsAPayloadBiggerThanTheLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := photos.NewLocalStore(dir, "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	var logBuf strings.Builder
	router, issuer := newPhotoRouter(t, store, &logBuf)
	token, err := issuer.Issue(identity.Claims{
		TenantID: photoTenantID,
		UserID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000f2"),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	content := make([]byte, photos.MaxPhotoBytes+1)
	body, contentType := photoMultipart(t, "image/jpeg", content)
	recorder := doPhotoRequest(t, router, http.MethodPost, "/photos", token, body, contentType)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422: un payload sobre el tope es un cuerpo ilegal (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := parsePhotoEnvelope(t, recorder)
	if envelope.Error.Code != httpx.CodeValidationError {
		t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
	}
	if entries, readErr := os.ReadDir(filepath.Join(dir, photoTenantID.String())); readErr == nil && len(entries) > 0 {
		t.Errorf("un payload rechazado escribió %d objetos igual", len(entries))
	}
}

// TestPhotoUploadRejectsABodyThatIsNotAMultipartPhoto: la foto viaja como multipart en el
// campo photo, con un Content-Type coherente. Cualquier otro cuerpo es 422 y no toca el
// store ni el disco.
func TestPhotoUploadRejectsABodyThatIsNotAMultipartPhoto(t *testing.T) {
	dir := t.TempDir()
	store, err := photos.NewLocalStore(dir, "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	router, issuer := newPhotoRouter(t, store, &strings.Builder{})
	token, err := issuer.Issue(identity.Claims{
		TenantID: photoTenantID,
		UserID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000f2"),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cases := map[string]struct {
		body        string
		contentType string
	}{
		"a form-urlencoded body": {body: "photo=arroz", contentType: "application/x-www-form-urlencoded"},
		"a wrong field name":     {body: "{\"photo\":\"x\"}", contentType: "application/json"},
		"an empty body":          {body: "", contentType: "multipart/form-data; boundary=no-such-boundary"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := doPhotoRequest(t, router, http.MethodPost, "/photos", token, tc.body, tc.contentType)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			envelope := parsePhotoEnvelope(t, recorder)
			if envelope.Error.Code != httpx.CodeValidationError {
				t.Errorf("error.code = %q, want %q", envelope.Error.Code, httpx.CodeValidationError)
			}
			if entries, readErr := os.ReadDir(dir); readErr == nil && len(entries) > 0 {
				t.Errorf("un cuerpo ilegal escribió %d objetos igual", len(entries))
			}
		})
	}
}

// TestPhotoUploadRejectsAnUnsupportedContentType: la lista de tipos es CERRADA (jpeg, png,
// webp); un gif —aunque sea una imagen que un navegador sabe mostrar— es 422 y el objeto
// ni se toca.
func TestPhotoUploadRejectsAnUnsupportedContentType(t *testing.T) {
	dir := t.TempDir()
	store, err := photos.NewLocalStore(dir, "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	router, issuer := newPhotoRouter(t, store, &strings.Builder{})
	token, err := issuer.Issue(identity.Claims{
		TenantID: photoTenantID,
		UserID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000f2"),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	body, contentType := photoMultipart(t, "image/gif", []byte("gif87a"))
	recorder := doPhotoRequest(t, router, http.MethodPost, "/photos", token, body, contentType)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("estado = %d, want 422 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	envelope := parsePhotoEnvelope(t, recorder)
	details, ok := envelope.Error.Details.(map[string]any)
	if !ok || details["field"] != "photo" {
		t.Errorf("details = %#v, want un objeto que nombre el campo photo", envelope.Error.Details)
	}
	if entries, readErr := os.ReadDir(dir); readErr == nil && len(entries) > 0 {
		t.Errorf("un tipo no listado escribió %d objetos igual", len(entries))
	}
}

// TestPhotoUploadNeedsAValidToken: la ruta es de negocio como cualquier otra: sin token o
// con uno inservible responde 401 y nada corre.
func TestPhotoUploadNeedsAValidToken(t *testing.T) {
	store, err := photos.NewLocalStore(t.TempDir(), "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	router, _ := newPhotoRouter(t, store, &strings.Builder{})

	body, contentType := photoMultipart(t, "image/jpeg", []byte{0x01})

	for name, token := range map[string]string{
		"no token":         "",
		"a fake one":       "no-soy-un-token",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := doPhotoRequest(t, router, http.MethodPost, "/photos", token, body, contentType)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("estado = %d, want 401 (cuerpo %q)", recorder.Code, recorder.Body.String())
			}
			if got := parsePhotoEnvelope(t, recorder).Error.Code; got != httpx.CodeUnauthenticated {
				t.Errorf("error.code = %q, want %q", got, httpx.CodeUnauthenticated)
			}
		})
	}
}

// TestPhotoUploadWithoutTheMiddlewareFailsClosed: un montaje sin middleware no puede leer
// un tenant inexistente —500, la misma granularidad del resto de las rutas autenticadas—.
func TestPhotoUploadWithoutTheMiddlewareFailsClosed(t *testing.T) {
	store, err := photos.NewLocalStore(t.TempDir(), "http://photos:9000")
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	var logBuf strings.Builder

	handler, err := photos.NewPhotosHandler(store, slog.New(slog.NewTextHandler(&logBuf, nil)))
	if err != nil {
		t.Fatalf("NewPhotosHandler: %v", err)
	}

	body, contentType := photoMultipart(t, "image/jpeg", []byte{0x01})
	recorder := doPhotoRequest(t, handler, http.MethodPost, "/photos", "", body, contentType)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500: sin claims no hay tenant al que subir la foto (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if got := parsePhotoEnvelope(t, recorder).Error.Code; got != httpx.CodeInternalError {
		t.Errorf("error.code = %q, want %q", got, httpx.CodeInternalError)
	}
	if !strings.Contains(logBuf.String(), "middleware") {
		t.Errorf("la causa no quedó registrada: %q", logBuf.String())
	}
}

// TestPhotoUploadFailsClosedWhenTheStoreIsBroken: un fallo del store de objetos es 500 con
// el sobre genérico; la causa queda solo en el registro del operador.
func TestPhotoUploadFailsClosedWhenTheStoreIsBroken(t *testing.T) {
	store := &stubStore{err: errors.New("the object storage dir is unwritable")}
	var logBuf strings.Builder
	router, issuer := newPhotoRouter(t, store, &logBuf)
	token, err := issuer.Issue(identity.Claims{
		TenantID: photoTenantID,
		UserID:   uuid.MustParse("019a0000-0000-7000-8000-0000000000f2"),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	body, contentType := photoMultipart(t, "image/jpeg", []byte{0x01})
	recorder := doPhotoRequest(t, router, http.MethodPost, "/photos", token, body, contentType)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("estado = %d, want 500 (cuerpo %q)", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "unwritable") {
		t.Errorf("el 500 filtró la causa interna: %q", recorder.Body.String())
	}
	if !strings.Contains(logBuf.String(), "unwritable") {
		t.Errorf("la causa no llegó al registro del operador: %q", logBuf.String())
	}
	if store.putCalls != 1 {
		t.Errorf("el store corrió %d vez/veces, want 1", store.putCalls)
	}
}
