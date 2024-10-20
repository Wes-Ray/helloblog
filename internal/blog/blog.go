package blog

import (
	// internal
	"blog/internal/users"
	"encoding/base64"
	"io"

	// golang
	"database/sql"
	// "encoding/base64"
	// "os"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"time"

	// externals
	"github.com/gorilla/sessions"
)

type BlogPage struct {
	Title    string
	Content  string // TODO: might want to make this markdown compatible
	PostTime time.Time
	Image    string // TODO: consider changing this back to a blob/save in a subfolder to serve
}

const (
	MAX_UPLOAD_SIZE int64 = 100 << 20 // 100 mb max upload size
)

// Render everything but base page/splash, uses template name for page title
func RenderTemplate(w http.ResponseWriter, r *http.Request, template_name string, data interface{}, st *sessions.CookieStore) {
	templ_path := filepath.Join("templates", template_name+".html")

	tmpl, err := template.ParseFiles("templates/base.html", templ_path)
	if err != nil {
		log.Printf("error parsing templates for '%v' page: %v", template_name, err)
		return
	}

	username, _ := users.GetCurrentUsername(r, st)

	content := map[string]interface{}{
		"Title": template_name,
		"Username": username,
		"Data":  data,
	}

	err = tmpl.ExecuteTemplate(w, "base.html", content)
	if err != nil {
		log.Printf("error rendering templates for '%v' page: %v", template_name, err)
		return
	}
}

func RenderSplash(w http.ResponseWriter, r *http.Request) {
	templ_path := filepath.Join("templates", "Splash.html")
	tmpl, err := template.ParseFiles(templ_path)
	if err != nil {		log.Printf("error parsing template for splash page: %v", err)
		return
	}

	err = tmpl.Execute(w, nil); if err != nil {
		log.Printf("error rendering template for splash page: %v", err)
		return
	}
}

func TestPage(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {

	data := map[string]interface{}{
		"session": users.GetSessionString(r, st),
	}
	RenderTemplate(w, r, "Test", data, st)
}

func ListPages(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	// TODO: sort by publish date, list on page
	rows, err := db.Query("SELECT title, post_time FROM blog")
	if err != nil {
		log.Printf("Failed to get pagefrom DB: %v", err)
		return
	}

	pages := []BlogPage{}

	for rows.Next() {
		var p BlogPage
		err := rows.Scan(&p.Title, &p.PostTime)
		if err != nil {
			log.Printf("Failed to scan row: %v", err)
			return
		}

		pages = append(pages, p)
	}

	RenderTemplate(w, r, "Index", pages, st)
}

func UploadPage(w http.ResponseWriter, r *http.Request, st *sessions.CookieStore) {
	// TODO: redirect to custom 404 page
	if !users.IsUploader(r, st) {
		log.Printf("non uploader attempted to access accounts page handler from: %v", r.Host)
		RenderTemplate(w, r, "NotFound", nil, st)	
		return
	}
	RenderTemplate(w, r, "Upload", nil, st)
}

// returns true if page already exists
func addPageToDB(db *sql.DB, title string, content string, post_time time.Time, image64 string) (error, bool) {
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM BLOG WHERE title = ?)", title).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if row exists: %w", err), false
	}
	if exists {
		return fmt.Errorf("'%s' already exists in db", title), true
	}

	stmt, err := db.Prepare("INSERT INTO blog (title, content, post_time, image) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err, false
	}
	defer stmt.Close()

	_, err = stmt.Exec(title, content, post_time, image64)
	if err != nil {
		return fmt.Errorf("failed to add to database: %w", err), false
	}

	return nil, false
}

func UploadHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	// TODO: redirect to custom 404 page
	if !users.IsUploader(r, st) {
		log.Printf("only uploaders can access this")
		RenderTemplate(w, r, "NotFound", nil, st)
		return
	}

	err := r.ParseMultipartForm(MAX_UPLOAD_SIZE) // 100 mb max upload size
	if err != nil {
		log.Printf("Error parsing form: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	log.Printf("Listing params (%d)", len(r.Form))
	for key, values := range r.Form {
		for _, value := range values {
			log.Printf("Received param: %s = %s", key, value)
		}
	}

	title := r.FormValue("title")
	if title == "" {
		log.Println("Client req'd upload of empty title")
		w.Write([]byte(`<div id="upload-status">Title is required</div>`))
		return
	}

	// TODO: add these as options
	content := "new test content"
	time := time.Now()

	// image_path := "images/unavailable.png"
	// bp, _ := getPageFromDB("asdf", db)
	file, header, err := r.FormFile("image")
	if err != nil {
		log.Printf("Error retrieving file on upload attempt: %v", err)
		w.Write([]byte(`<div id="upload-status">Error uploading file</div>`))
		return
	}
	defer file.Close()
	file_bytes, err := io.ReadAll(file)
	if err != nil {
		log.Printf("Error reading file: %v", err)
		w.Write([]byte(`<div id="upload-status">Error reading file</div>`))
		return
	}

	_ = header
	file64 := base64.StdEncoding.EncodeToString(file_bytes)
	// file64 = fmt.Sprintf("data:%s;base64,%s", header.Header.Get("Content-Type"), file64)

	err, exists := addPageToDB(db, title, content, time, file64)
	if err != nil {
		log.Printf("Error adding %s to db: %v", title, err)
		if exists {
			w.Write([]byte(`<div id="upload-status">Title already in use.</div>`))
		} else {
			w.Write([]byte(`<div id="upload-status">Error uploading file to database</div>`))
		}
		return
	}
	// TODO: redirect uploader to uploaded page
	log.Printf("Successfully added %s to database", title)
}

func AccountsPageHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	// TODO: redirect to custom 404 page
	if !users.IsAdmin(r, st) {
		log.Printf("non admin attempted to access accounts page handler from: %v", r.Host)
		RenderTemplate(w, r, "NotFound", nil, st)
		return
	}

	users, err := users.GetUsers(db)
	if err != nil {
		log.Printf("error getting users: %v", err)
	}

	RenderTemplate(w, r, "Accounts", users, st)
}

func DeletePageHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsAdmin(r, st) {
		log.Printf("non admin attempted to use delete handler: %v", r.Host)
		return
	}	

	err := r.ParseForm()
	if err != nil {
		log.Printf("Error parsing form: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	title := r.Form.Get("title")
	if title == "" {
		log.Printf("couldn't get title when deleting")
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	stmt, err := db.Prepare("DELETE FROM blog WHERE title = ?")
	if err != nil {
		log.Printf("error deleting %v from db", title)
		return
	}
	defer stmt.Close()

	result, err := stmt.Exec(title)
	if err != nil {
		log.Printf("failed to execute delete for %v: %v", title, err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Check if a row was affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("failed to retrieve affected rows: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if rowsAffected == 0 {
		log.Printf("no row found with the title: %s", title)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	w.Header().Set("HX-Redirect", "/index")
}

func getPageFromDB(title string, db *sql.DB) (*BlogPage, error) {
	query := "SELECT title, content, post_time, image FROM blog WHERE title = ?"

	row := db.QueryRow(query, title)
	var p BlogPage

	// TODO: update so it gives a different err for it being missing from database vs some other issue
	err := row.Scan(&p.Title, &p.Content, &p.PostTime, &p.Image)
	if err != nil {
		return nil, err
	}

	return &p, err
}

func GetRequest(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsAuthed(r, st) {
		log.Println("accessing GetRequest without auth")
		RenderSplash(w, r)
		return
	}

	title := r.URL.Path[len("/"):]
	if title == "" {
		log.Println("Requesting root")
		// TODO: redirect to home/most recent page or something
		ListPages(w, r, db, st)
		return
	}
	// log.Printf("Requested page: %s", title)

	p, err := getPageFromDB(title, db)
	if err != nil {
		log.Printf("Error getting generic page '%v' from database: %v", title, err)
		RenderTemplate(w, r, "NotFound", nil, st)
		return
	}

	// Rendering page with template (different case than RenderTemplate)

	tmpl, err := template.ParseFiles("templates/base.html", "templates/Page.html")
	if err != nil {
		log.Printf("error parsing templates for blog page: %v", err)
		return
	}

	username, _ := users.GetCurrentUsername(r, st)
	admin := ""
	if users.IsAdmin(r, st) {
		admin = "admin"
	}
	uploader := ""
	if users.IsUploader(r, st) {
		uploader = "uploader"
	}

	content := map[string]interface{}{
		"Title": p.Title,
		"Username": username,
		"Admin": admin,
		"Uploader": uploader,
		"Data":  p,
	}

	err = tmpl.ExecuteTemplate(w, "base.html", content)
	if err != nil {
		log.Printf("error rendering templates for test page: %v", err)
		return
	}
}
