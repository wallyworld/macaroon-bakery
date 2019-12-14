// This code is copied across from github.com/juju/httprequest since we
// need to migrate elsewhere to using gopkg.in/juju/httprequest.v1.
// We also copy code from github.com/julienschmidt/httprouter to
// avoid go dep vendoring conflicts.

package httpbakery

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gopkg.in/errgo.v1"
)

type ErrorMapper func(err error) (httpStatus int, errorBody interface{})

// Params holds the parameters provided to an HTTP request.
type Params struct {
	Response http.ResponseWriter
	Request  *http.Request
	PathVar  URLParams
	// PathPattern holds the path pattern matched by httprouter.
	// It is only set where httprequest has the information;
	// that is where the call was made by ErrorMapper.Handler
	// or ErrorMapper.Handlers.
	PathPattern string
}

// JSONHandler is like httprouter.Handle except that it returns a
// body (to be converted to JSON) and an error.
// The Header parameter can be used to set
// custom headers on the response.
type JSONHandler func(Params) (interface{}, error)

// ErrorHandler is like httprouter.Handle except it returns an error
// which may be returned as the error body of the response.
// An ErrorHandler function should not itself write to the ResponseWriter
// if it returns an error.
type ErrorHandler func(Params) error

// HandleJSON returns a handler that writes the return value of handle
// as a JSON response. If handle returns an error, it is passed through
// the error mapper.
//
// Note that the Params argument passed to handle will not
// have its PathPattern set as that information is not available.
func (e ErrorMapper) HandleJSON(handle JSONHandler) Handle {
	return func(w http.ResponseWriter, req *http.Request, p URLParams) {
		val, err := handle(Params{
			Response: headerOnlyResponseWriter{w.Header()},
			Request:  req,
			PathVar:  p,
		})
		if err == nil {
			if err = WriteJSON(w, http.StatusOK, val); err == nil {
				return
			}
		}
		e.WriteError(w, err)
	}
}

// HandleErrors returns a handler that passes any non-nil error returned
// by handle through the error mapper and writes it as a JSON response.
//
// Note that the Params argument passed to handle will not
// have its PathPattern set as that information is not available.
func (e ErrorMapper) HandleErrors(handle ErrorHandler) Handle {
	return func(w http.ResponseWriter, req *http.Request, p URLParams) {
		w1 := responseWriter{
			ResponseWriter: w,
		}
		if err := handle(Params{
			Response: &w1,
			Request:  req,
			PathVar:  p,
		}); err != nil {
			if w1.headerWritten {
				// The header has already been written,
				// so we can't set the appropriate error
				// response code and there's a danger
				// that we may be corrupting the
				// response by appending a JSON error
				// message to it.
				// TODO log an error in this case.
				return
			}
			e.WriteError(w, err)
		}
	}
}

// WriteError writes an error to a ResponseWriter
// and sets the HTTP status code.
//
// It uses WriteJSON to write the error body returned from
// the ErrorMapper so it is possible to add custom
// headers to the HTTP error response by implementing
// HeaderSetter.
func (e ErrorMapper) WriteError(w http.ResponseWriter, err error) {
	status, resp := e(err)
	err1 := WriteJSON(w, status, resp)
	if err1 == nil {
		return
	}
	// TODO log an error ?

	// JSON-marshaling the original error failed, so try to send that
	// error instead; if that fails, give up and go home.
	status1, resp1 := e(errgo.Notef(err1, "cannot marshal error response %q", err))
	err2 := WriteJSON(w, status1, resp1)
	if err2 == nil {
		return
	}

	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(fmt.Sprintf("really cannot marshal error response %q: %v", err, err1)))
}

// WriteJSON writes the given value to the ResponseWriter
// and sets the HTTP status to the given code.
//
// If val implements the HeaderSetter interface, the SetHeader
// method will be called to add additional headers to the
// HTTP response. It is called after the Content-Type header
// has been added, so can be used to override the content type
// if required.
func WriteJSON(w http.ResponseWriter, code int, val interface{}) error {
	// TODO consider marshalling directly to w using json.NewEncoder.
	// pro: this will not require a full buffer allocation.
	// con: if there's an error after the first write, it will be lost.
	data, err := json.Marshal(val)
	if err != nil {
		return errgo.Mask(err)
	}
	w.Header().Set("content-type", "application/json")
	if headerSetter, ok := val.(HeaderSetter); ok {
		headerSetter.SetHeader(w.Header())
	}
	w.WriteHeader(code)
	w.Write(data)
	return nil
}

// HeaderSetter is the interface checked for by WriteJSON.
// If implemented on a value passed to WriteJSON, the SetHeader
// method will be called to allow it to set custom headers
// on the response.
type HeaderSetter interface {
	SetHeader(http.Header)
}

// Ensure statically that responseWriter does implement http.Flusher.
var _ http.Flusher = (*responseWriter)(nil)

// responseWriter wraps http.ResponseWriter but allows us
// to find out whether any body has already been written.
type responseWriter struct {
	headerWritten bool
	http.ResponseWriter
}

func (w *responseWriter) Write(data []byte) (int, error) {
	w.headerWritten = true
	return w.ResponseWriter.Write(data)
}

func (w *responseWriter) WriteHeader(code int) {
	w.headerWritten = true
	w.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher.Flush.
func (w *responseWriter) Flush() {
	w.headerWritten = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type headerOnlyResponseWriter struct {
	h http.Header
}

func (w headerOnlyResponseWriter) Header() http.Header {
	return w.h
}

func (w headerOnlyResponseWriter) Write([]byte) (int, error) {
	return 0, errgo.New("inappropriate call to ResponseWriter.Write in JSON-returning handler")
}

func (w headerOnlyResponseWriter) WriteHeader(code int) {
}

// Handle is a function that can be registered to a route to handle HTTP
// requests. Like http.HandlerFunc, but has a third parameter for the values of
// wildcards (variables).
type Handle func(http.ResponseWriter, *http.Request, URLParams)

// Param is a single URL parameter, consisting of a key and a value.
type URLParam struct {
	Key   string
	Value string
}

// URLParams is a URLParam-slice, as returned by the router.
// The slice is ordered, the first URL parameter is also the first slice value.
// It is therefore safe to read values by the index.
type URLParams []URLParam
