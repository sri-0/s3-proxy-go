package proxy

import (
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sri/s3-proxy-go/internal/backend"
	"github.com/sri/s3-proxy-go/internal/catalog"
	"github.com/sri/s3-proxy-go/internal/config"
)

func BuildRouter(cfg *config.Config, backends map[string]backend.Backend, cat catalog.Catalog) *mux.Router {
	router := mux.NewRouter()
	allowedTags := cfg.AllowedTagSet()
	excludedHeaders := cfg.ExcludedHeaderSet()

	for _, acc := range cfg.Accounts {
		be := backends[acc.Name]

		if len(acc.Buckets) > 0 {
			for _, bkt := range acc.Buckets {
				rc := &RouteConfig{
					Backend:             be,
					AccountName:         acc.Name,
					Bucket:              bkt.Name,
					AllowedOps:          config.OpsSet(bkt.ResolveOps(acc.AllowedOperations)),
					AllowedTags:         allowedTags,
					MandatoryHeaders:    cfg.MandatoryPutHeaders,
					SecurityTagHeader:   cfg.SecurityTagHeader,
					ExcludedMetaHeaders: excludedHeaders,
					Catalog:             cat,
				}

				prefix := acc.PathPrefix + "/" + bkt.Name
				registerRoutes(router, prefix, rc)

				log.Printf("route %s/%s at %s ops=%v",
					acc.Name, bkt.Name, prefix, bkt.ResolveOps(acc.AllowedOperations))
			}
		} else {
			rc := &RouteConfig{
				Backend:             be,
				AccountName:         acc.Name,
				AllowedOps:          config.OpsSet(acc.AllowedOperations),
				AllowedTags:         allowedTags,
				MandatoryHeaders:    cfg.MandatoryPutHeaders,
				SecurityTagHeader:   cfg.SecurityTagHeader,
				ExcludedMetaHeaders: excludedHeaders,
				Catalog:             cat,
			}

			prefix := acc.PathPrefix + "/{bucket}"
			registerRoutes(router, prefix, rc)

			log.Printf("route %s at %s/* ops=%v",
				acc.Name, acc.PathPrefix, acc.AllowedOperations)
		}
	}

	if cat != nil {
		catalogHandler := catalog.NewHandler(cat, cfg.SecurityTagHeader)
		router.HandleFunc("/catalog/objects/{bucket}/{key:.+}", catalogHandler.HandleObjectHistory).Methods("GET")
		router.HandleFunc("/catalog/objects", catalogHandler.HandleQuery).Methods("GET")
		router.HandleFunc("/catalog/stats", catalogHandler.HandleStats).Methods("GET")
		log.Printf("catalog query endpoints registered at /catalog/*")
	}

	return router
}

func registerRoutes(router *mux.Router, prefix string, rc *RouteConfig) {
	router.HandleFunc(prefix+"/{key:.+}", objectHandler(rc)).
		Methods("GET", "PUT", "DELETE", "HEAD")
	router.HandleFunc(prefix+"/", listHandler(rc)).Methods("GET")
	router.HandleFunc(prefix, listHandler(rc)).Methods("GET")
}

func objectHandler(rc *RouteConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opName := methodToOp(r.Method)
		if !rc.AllowedOps[opName] {
			http.Error(w, "Operation not permitted: "+opName, http.StatusMethodNotAllowed)
			return
		}

		switch r.Method {
		case http.MethodGet:
			handleGetObject(rc, w, r)
		case http.MethodPut:
			handlePutObject(rc, w, r)
		case http.MethodDelete:
			handleDeleteObject(rc, w, r)
		case http.MethodHead:
			handleHeadObject(rc, w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func listHandler(rc *RouteConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rc.AllowedOps["LIST"] {
			http.Error(w, "Operation not permitted: LIST", http.StatusMethodNotAllowed)
			return
		}
		handleListObjects(rc, w, r)
	}
}

func methodToOp(method string) string {
	switch method {
	case http.MethodGet:
		return "GET"
	case http.MethodPut:
		return "PUT"
	case http.MethodDelete:
		return "DELETE"
	case http.MethodHead:
		return "HEAD"
	default:
		return method
	}
}
