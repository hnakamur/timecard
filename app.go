package timecard

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"appengine"
	"appengine/datastore"
	"appengine/user"
)

type User struct {
	Email   string
	Name    string
	Enabled bool
}

func userKey(c appengine.Context) *datastore.Key {
	return datastore.NewKey(c, "User", "default_user", 0, nil)
}

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
			c.Errorf("%v", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		redirect(w, url)
		return
	}

	if e := fn(c, w, r); e != nil {
		handleAppError(c, w, e)
	}
}

func handleAppError(c appengine.Context, w http.ResponseWriter, e *appError) {
	c.Errorf("%v", e.Error)
	http.Error(w, e.Message, e.Code)
}

type apiHandler func(appengine.Context, http.ResponseWriter, *http.Request) (jsonData interface{}, error *appError)

func (fn apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	u := user.Current(c)
	if u == nil {
		err := errors.New("login needed")
		c.Errorf("%v", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonData, appErr := fn(c, w, r)
	if appErr != nil {
		handleAppError(c, w, appErr)
	}

	err := writeJsonResponse(w, jsonData)
	if err != nil {
		c.Errorf("%v", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJsonResponse(w http.ResponseWriter, jsonData interface{}) error {
	w.Header().Add("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	return encoder.Encode(jsonData)
}

func redirect(w http.ResponseWriter, url string) {
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusFound)
}

func init() {
	http.Handle("/", appHandler(rootHandler))
	http.Handle("/my/arrivals", appHandler(myArrivalsHandler))
	http.Handle("/my/leaves", appHandler(myLeavesHandler))

	http.Handle("/api/admin/users", apiHandler(apiAdminUsersHandler))
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

func apiAdminUsersHandler(c appengine.Context, w http.ResponseWriter, r *http.Request) (interface{}, *appError) {
	if r.Method == "GET" {
		q := datastore.NewQuery("User").Ancestor(punchKey(c)).Order("Name")
		var users []User
		if _, err := q.GetAll(c, &users); err != nil {
			return nil, &appError{
				Error:   err,
				Message: "Failed to fetch users data from the datastore",
				Code:    http.StatusInternalServerError,
			}
		}

		var jsonUsers []interface{}
		for _, user := range users {
			jsonUsers = append(jsonUsers, map[string]interface{}{
				"email":   user.Email,
				"name":    user.Name,
				"enabled": user.Enabled,
			})
		}

		return map[string]interface{}{
			"users": jsonUsers,
		}, nil

	} else if r.Method == "POST" {
		enabled, appErr := getFormBoolValue(r, "enabled", true)
		if appErr != nil {
			return nil, appErr
		}

		c.Debugf("formvalues. email=%s, name=%s", r.FormValue("email"), r.FormValue("name"))
		u := User{
			Email:   r.FormValue("email"),
			Name:    r.FormValue("name"),
			Enabled: enabled,
		}
		key := datastore.NewIncompleteKey(c, "User", punchKey(c))
		_, err := datastore.Put(c, key, &u)
		if err != nil {
			return nil, &appError{
				Error:   err,
				Message: "Failed to put a user data to the datastore",
				Code:    http.StatusInternalServerError,
			}
		}

		return map[string]interface{}{
			"user": map[string]interface{}{
				"email":   r.FormValue("email"),
				"name":    u.Name,
				"enabled": u.Enabled,
			},
		}, nil
	} else {
		err := errors.New("Unsupported http method")
		return nil, &appError{
			Error:   err,
			Message: err.Error(),
			Code:    http.StatusBadRequest,
		}
	}
}

func getFormBoolValue(r *http.Request, name string, defaultValue bool) (bool, *appError) {
	boolValue := defaultValue
	strValue := r.FormValue(name)
	if strValue != "" {
		var err error
		boolValue, err = strconv.ParseBool(strValue)
		if err != nil {
			return false, &appError{
				Error:   err,
				Message: fmt.Sprintf(`Failed to parse the "%s" parameter as a boolean value`, name),
				Code:    http.StatusBadRequest,
			}
		}
	}
	return boolValue, nil
}
