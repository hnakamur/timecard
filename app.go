package hello

import (
	"html/template"
	"net/http"
	"time"

	"appengine"
	"appengine/datastore"
	"appengine/user"
)

type Punch struct {
	Puncher string
	Type    string
	Time    time.Time
}

func punchKey(c appengine.Context) *datastore.Key {
	return datastore.NewKey(c, "Punch", "default_punch", 0, nil)
}

// See http://blog.golang.org/error-handling-and-go

type appError struct {
	Error   error
	Message string
	Code    int
}

type appHandler func(appengine.Context, http.ResponseWriter, *http.Request) *appError

func (fn appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	u := user.Current(c)
	if u == nil {
		url, err := user.LoginURL(c, r.URL.String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		redirect(w, url)
		return
	}

	if e := fn(c, w, r); e != nil {
		c.Errorf("%v", e.Error)
		http.Error(w, e.Message, e.Code)
	}
}

func redirect(w http.ResponseWriter, url string) {
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusFound)
}

func init() {
	http.Handle("/", appHandler(rootHandler))
	http.Handle("/my/arrivals", appHandler(myArrivalsHandler))
	http.Handle("/my/leaves", appHandler(myLeavesHandler))
}

func rootHandler(c appengine.Context, w http.ResponseWriter, r *http.Request) *appError {
	u := user.Current(c)
	q := datastore.NewQuery("Punch").Ancestor(punchKey(c)).Order("Time").Limit(10)
	punches := make([]Punch, 0, 10)
	if _, err := q.GetAll(c, &punches); err != nil {
		return &appError{
			Error:   err,
			Message: "Failed to fetch punches data from the datastore",
			Code:    http.StatusInternalServerError,
		}
	}
	data := map[string]interface{}{
		"User":    u,
		"Punches": punches,
	}
	if err := rootTemplate.Execute(w, data); err != nil {
		return &appError{
			Error:   err,
			Message: "Failed to execute the root template",
			Code:    http.StatusInternalServerError,
		}
	}
	return nil
}

func formatDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04")
}

var templateFuncs = template.FuncMap{
	"formatDateTime": formatDateTime,
}

var rootTemplate = template.Must(template.New("root").Funcs(templateFuncs).Parse(`
<html>
  <head>
    <title>Timecard</title>
  </head>
  <body>
    <div>Hello, {{.User}}!</div>
    <ul>
    {{range .Punches}}
      <li>{{.Type}} {{formatDateTime .Time}}</li>
    {{end}}
    </ul>
    <form action="/my/arrivals" method="post">
      <input type="submit" value="Arrive">
    </form>
    <form action="/my/leaves" method="post">
      <input type="submit" value="Leave">
    </form>
  </body>
</html>
`))

func myArrivalsHandler(c appengine.Context, w http.ResponseWriter, r *http.Request) *appError {
	if r.Method == "POST" {
		err := createPunch(c, "arrival")
		if err != nil {
			return err
		}
		redirect(w, "/")
	}
	return nil
}

func myLeavesHandler(c appengine.Context, w http.ResponseWriter, r *http.Request) *appError {
	if r.Method == "POST" {
		err := createPunch(c, "leave")
		if err != nil {
			return err
		}
		redirect(w, "/")
	}
	return nil
}

func createPunch(c appengine.Context, punchType string) *appError {
	u := user.Current(c)
	p := Punch{
		Puncher: u.Email,
		Type:    punchType,
		Time:    time.Now(),
	}
	key := datastore.NewIncompleteKey(c, "Punch", punchKey(c))
	_, err := datastore.Put(c, key, &p)
	if err != nil {
		return &appError{
			Error:   err,
			Message: "Failed to put a punch data to the datastore",
			Code:    http.StatusInternalServerError,
		}
	}
	return nil
}
