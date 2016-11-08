package server

import (
	"net/http"
	"regexp"

	"github.com/kevinburke/rest"
)

type searchServer struct{}

var smsSid = regexp.MustCompile("^" + messagePattern + "$")
var callSid = regexp.MustCompile("^" + callPattern + "$")
var conferenceSid = regexp.MustCompile("^" + conferencePattern + "$")

func (s *searchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	q := query.Get("q")
	if q == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if smsSid.MatchString(q) {
		http.Redirect(w, r, "/messages/"+q, http.StatusMovedPermanently)
		return
	}
	if callSid.MatchString(q) {
		http.Redirect(w, r, "/calls/"+q, http.StatusMovedPermanently)
		return
	}
	if conferenceSid.MatchString(q) {
		http.Redirect(w, r, "/conferences/"+q, http.StatusMovedPermanently)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

type openSearchXMLServer struct {
	PublicHost              string
	AllowUnencryptedTraffic bool
}

type searchData struct {
	Scheme     string
	PublicHost string
}

func (o *openSearchXMLServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if o.PublicHost == "" {
		rest.NotFound(w, r)
		return
	}
	var scheme string
	if o.AllowUnencryptedTraffic {
		scheme = "http"
	} else {
		scheme = "https"
	}
	data := &searchData{
		Scheme:     scheme,
		PublicHost: o.PublicHost,
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	if err := openSearchTemplate.Execute(w, data); err != nil {
		rest.ServerError(w, r, err)
	}
}
