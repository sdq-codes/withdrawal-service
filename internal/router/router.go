package router

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	httpapi "github.com/sdq-codes/withdrawal-service/internal/interface/http"
	"github.com/sdq-codes/withdrawal-service/internal/router/middleware"
)

func New(deps httpapi.Dependencies) http.Handler {
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, deps)

	var handler http.Handler = middleware.AccessLog(deps.Logger, mux)
	handler = middleware.RequestTimeout(5*time.Second, handler)
	handler = middleware.SecurityHeaders(handler)
	return otelhttp.NewHandler(handler, "http.server")
}
