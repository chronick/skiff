package daemon

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func (d *Daemon) setupProxy(mux *http.ServeMux) {
	if d.cfg.Proxy == nil {
		return
	}

	for _, route := range d.cfg.Proxy.Routes {
		target := route
		prefix := target.Path
		targetURL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", target.Port))

		proxy := httputil.NewSingleHostReverseProxy(targetURL)

		mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			proxy.ServeHTTP(w, r)
		})
	}
}
