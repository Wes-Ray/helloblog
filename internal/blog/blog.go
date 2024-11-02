package blog

import (
	// internal
	"blog/internal/users"
	"encoding/base64"
	"io"
	"slices"
	"strings"
	"strconv"

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
	ID       int64
	Title    string
	Content  string // TODO: might want to make this markdown compatible
	PostTime time.Time
	Image    string // TODO: consider changing this back to a blob/save in a subfolder to serve
	Tags     []Tag
	Comments []Comment
}

type Comment struct {
    ID        int64
    PageID    int64
    Username  sql.NullString
    Content   string
    PostTime  time.Time
}

type Tag struct {
	ID   		int64
	Name 		string
	Selected 	bool
}

const (
	MAX_UPLOAD_SIZE int64 = 100 << 20 // 100 mb max upload size, ensure nginx server config is set to match/exceed
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
		"Title":    template_name,
		"Username": username,
		"Data":     data,
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
	if err != nil {
		log.Printf("error parsing template for splash page: %v", err)
		return
	}

	err = tmpl.Execute(w, nil)
	if err != nil {
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

func IndexPage(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {

	// get tags, format: /index/tags=TAG
	tag_format := "/index/tags="
	tag_names := []string{}

	if len(r.URL.Path) > len(tag_format) {
		// tags exist
		tags_query := r.URL.Path[len(tag_format):]
		tag_names = strings.Split(tags_query, ",")
	}

	// TODO: sort by publish date, list on page
	var rows *sql.Rows
	var err error
	if len(tag_names) == 0 {
		rows, err = db.Query("SELECT id, title, post_time FROM pages")
		if err != nil {
			log.Printf("Failed to get pagefrom DB: %v", err)
			return
		}
	} else {  // tags exist, filter by pages that contain all tags
		query := `
			SELECT DISTINCT p.id, p.title, p.post_time
			FROM pages p
			JOIN page_tags pt ON p.id = pt.page_id
			JOIN tags t ON pt.tag_id = t.id
			WHERE t.name IN (?` + strings.Repeat(",?", len(tag_names)-1) + `)
			GROUP BY p.id
			HAVING COUNT(DISTINCT t.name) = ?
			ORDER BY p.post_time DESC
		`

		args := make([]interface{}, len(tag_names)+1)
		for i, name := range tag_names {
			args[i] = name
		}
		args[len(tag_names)] = len(tag_names)

		rows, err = db.Query(query, args...); if err != nil {
			log.Printf("failed to access rows with tags '%v': %v", tag_names, err)
			RenderTemplate(w, r, "NotFound", nil, st)
			return
		}

	}
	defer rows.Close()



	pages := []BlogPage{}

	for rows.Next() {
		var p BlogPage
		err := rows.Scan(&p.ID, &p.Title, &p.PostTime)
		if err != nil {
			log.Printf("Failed to scan row: %v", err)
			return
		}

		pages = append(pages, p)
	}

	// get all tags from DB
	tag_rows, err := db.Query(
		`SELECT name
		FROM tags
		ORDER BY name
		`)
	if err != nil {
		log.Printf("failed to get all tags: %v", err)
		return
	}
	defer tag_rows.Close()

	tags := []Tag{}

	for tag_rows.Next() {
		var t Tag
		err = tag_rows.Scan(&t.Name); if err != nil {
			log.Printf("failed to scan row for tag: %v", err)
			return
		}
		t.Selected = slices.Contains(tag_names, t.Name)
		tags = append(tags, t)
	}

	data := struct {
		Pages []BlogPage
		Tags []Tag
	}{
		Pages: pages,
		Tags: tags,
	}

	RenderTemplate(w, r, "Index", data, st)
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
func addPageToDB(db *sql.DB, title string, content string, post_time time.Time, image64 string, tags []string) (error, bool) {
	// Start a transaction since we'll be doing multiple operations
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err), false
	}
	defer tx.Rollback() // Will rollback if we don't commit

	// Check if page exists
	var exists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pages WHERE title = ?)", title).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if row exists: %w", err), false
	}
	if exists {
		return fmt.Errorf("'%s' already exists in db", title), true
	}

	// Insert the page and get its ID
	result, err := tx.Exec("INSERT INTO pages (title, content, post_time, image) VALUES (?, ?, ?, ?)",
		title, content, post_time, image64)
	if err != nil {
		return fmt.Errorf("failed to add to database: %w", err), false
	}

	pageID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err), false
	}

	// Add tags
	for _, tagName := range tags {
		if tagName == "" {
			continue // Skip empty tags
		}

		// Insert tag if it doesn't exist and get its ID
		var tagID int64
		err = tx.QueryRow(`
            INSERT INTO tags (name) 
            VALUES (?) 
            ON CONFLICT(name) DO UPDATE SET name=name 
            RETURNING id`, tagName).Scan(&tagID)
		if err != nil {
			return fmt.Errorf("failed to insert/get tag '%s': %w", tagName, err), false
		}

		// Link tag to page
		_, err = tx.Exec(`
            INSERT INTO page_tags (page_id, tag_id) 
            VALUES (?, ?)`, pageID, tagID)
		if err != nil {
			return fmt.Errorf("failed to link tag '%s' to page: %w", tagName, err), false
		}
	}

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err), false
	}

	return nil, false
}

func UploadHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsUploader(r, st) {
		w.Write([]byte("Unauthorized access"))
		return
	}

	err := r.ParseMultipartForm(MAX_UPLOAD_SIZE)
	if err != nil {
		w.Write([]byte("Invalid request - file may be too large"))
		return
	}

	title := r.FormValue("title")
	if title == "" {
		w.Write([]byte("Title is required"))
		return
	}

	tags_string := r.FormValue("tags")
	// parse tags string into tags list 
	tags := strings.Fields(strings.ReplaceAll(tags_string, ",", " "))

	content := "new test content"
	time := time.Now()

	file, header, err := r.FormFile("image")
	if err != nil {
		w.Write([]byte("No image found"))
		return
	}
	defer file.Close()

	file_bytes, err := io.ReadAll(file)
	if err != nil {
		w.Write([]byte("Error reading file"))
		return
	}

	_ = header
	file64 := base64.StdEncoding.EncodeToString(file_bytes)

	err, exists := addPageToDB(db, title, content, time, file64, tags)
	if err != nil {
		if exists {
			w.Write([]byte("Title already in use"))
		} else {
			w.Write([]byte("Error uploading to database"))
			log.Printf("Error uploading to database: %v", err)
		}
		return
	}

	w.Write([]byte("Upload successful!"))
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

    tx, err := db.Begin()
    if err != nil {
        log.Printf("error starting transaction: %v", err)
        http.Error(w, "Internal server error", http.StatusInternalServerError)
        return
    }
    defer tx.Rollback()
    
    // First get the page ID
    var pageID int64
    err = tx.QueryRow("SELECT id FROM pages WHERE title = ?", title).Scan(&pageID)
    if err != nil {
        log.Printf("error getting page ID: %v", err)
        return
    }

    _, err = tx.Exec("DELETE FROM page_tags WHERE page_id = ?", pageID)
    if err != nil {
        log.Printf("error deleting page_tags: %v", err)
        return
    }

    stmt, err := tx.Prepare("DELETE FROM pages WHERE title = ?")
    if err != nil {
        log.Printf("error preparing delete statement: %v", err)
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

    // Clean up unused tags
    _, err = tx.Exec(`
        DELETE FROM tags 
        WHERE NOT EXISTS (
            SELECT 1 
            FROM page_tags 
            WHERE page_tags.tag_id = tags.id
        )
    `)
    if err != nil {
        log.Printf("error cleaning up tags: %v", err)
        return
    }

    err = tx.Commit()
    if err != nil {
        log.Printf("error committing transaction: %v", err)
        return
    }

    w.Header().Set("HX-Redirect", "/index")
}

func GetPostTags(ID int64, db *sql.DB) ([]Tag, error) {
	rows, err := db.Query(
		`
		SELECT t.id, t.name
		FROM tags t
		JOIN page_tags pt on t.id = pt.tag_id
		WHERE pt.page_id = ?
		ORDER BY t.name
		`, ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Name); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

func getPageFromDB(title string, db *sql.DB) (*BlogPage, error) {
	query := "SELECT id, title, content, post_time, image FROM pages WHERE title = ?"

	row := db.QueryRow(query, title)
	var p BlogPage

	// TODO: update so it gives a different err for it being missing from database vs some other issue
	err := row.Scan(&p.ID, &p.Title, &p.Content, &p.PostTime, &p.Image)
	if err != nil {
		return nil, err
	}

	// get tags from DB
	p.Tags, err = GetPostTags(p.ID, db)
	if err != nil {
		return nil, fmt.Errorf("error getting tags for '%v': %v", title, err)
	}

	comments, err := getCommentsForPage(db, p.ID)
    if err != nil {
        return nil, fmt.Errorf("error getting comments for '%v': %v", title, err)
    }
    p.Comments = comments

	return &p, nil
}

//
// Comments
//

func addComment(db *sql.DB, pageID int64, username string, content string) error {
    query := `
        INSERT INTO comments (page_id, username, content)
        VALUES (?, ?, ?)`
    
    var usernameArg interface{}
    if username == "" {
        usernameArg = nil
    } else {
        usernameArg = username
    }
    
    _, err := db.Exec(query, pageID, usernameArg, content)
    return err
}

func AddCommentHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
    err := r.ParseForm()
    if err != nil {
        log.Printf("Error parsing form: %v", err)
        http.Error(w, "Invalid request", http.StatusBadRequest)
        return
    }
    
    pageID := r.Form.Get("page_id")
    content := r.Form.Get("content")
    
    if pageID == "" || content == "" {
        http.Error(w, "Missing required fields", http.StatusBadRequest)
        return
    }
    
    pageIDInt, err := strconv.ParseInt(pageID, 10, 64)
    if err != nil {
        http.Error(w, "Invalid page ID", http.StatusBadRequest)
        return
    }
    
    username, _ := users.GetCurrentUsername(r, st)
    
    err = addComment(db, pageIDInt, username, content)
    if err != nil {
        log.Printf("Error adding comment: %v", err)
        http.Error(w, "Failed to add comment", http.StatusInternalServerError)
        return
    }
    
    // Refresh the comments section
    comments, err := getCommentsForPage(db, pageIDInt)
    if err != nil {
        log.Printf("Error getting comments: %v", err)
        http.Error(w, "Failed to refresh comments", http.StatusInternalServerError)
        return
    }
    
    tmpl, err := template.ParseFiles("templates/Comments.html")
    if err != nil {
        log.Printf("Error parsing template: %v", err)
        http.Error(w, "Template error", http.StatusInternalServerError)
        return
    }
    
    err = tmpl.Execute(w, comments)
    if err != nil {
        log.Printf("Error executing template: %v", err)
        http.Error(w, "Template error", http.StatusInternalServerError)
        return
    }
}



func getCommentsForPage(db *sql.DB, pageID int64) ([]Comment, error) {
    query := `
        SELECT id, page_id, username, content, post_time
        FROM comments
        WHERE page_id = ?
        ORDER BY post_time DESC`
        
    rows, err := db.Query(query, pageID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    
    var comments []Comment
    for rows.Next() {
        var c Comment
        err := rows.Scan(&c.ID, &c.PageID, &c.Username, &c.Content, &c.PostTime)
        if err != nil {
            return nil, err
        }
        comments = append(comments, c)
    }
    return comments, nil
}

func RootRequest(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsAuthed(r, st) {
		log.Println("accessing RootRequest without auth")
		RenderSplash(w, r)
		return
	}
	// TODO: consider redirecting to most recent page instead
	IndexPage(w, r, db, st)
}

func PageRequest(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsAuthed(r, st) {
		log.Println("accessing PageRequest without auth")
		RenderSplash(w, r)
		return
	}

	title := r.URL.Path[len("/page/"):]
	if title == "" {
		log.Println("Requesting root")
		// TODO: redirect to home/most recent page or something
		IndexPage(w, r, db, st)
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

	tmpl, err := template.ParseFiles("templates/base.html", "templates/Page.html", "templates/Comments.html")
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
		"Title":    p.Title,
		"Username": username,
		"Admin":    admin,
		"Uploader": uploader,
		"Data":     p,
	}

	err = tmpl.ExecuteTemplate(w, "base.html", content)
	if err != nil {
		log.Printf("error rendering templates for test page: %v", err)
		return
	}
}
