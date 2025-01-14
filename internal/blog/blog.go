package blog

import (
	// internal
	"blog/internal/users"
	"bytes"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strconv"
	"strings"

	// golang
	"database/sql"
	// "encoding/base64"
	"os"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"time"

	// externals
	"github.com/disintegration/imaging"
	"github.com/gorilla/sessions"
)

type BlogPage struct {
	ID       int64
	Title    string
	Content  string // TODO: might want to make this markdown compatible
	PostTime time.Time
	Image    string // TODO: consider changing this back to a blob/save in a subfolder to serve
	Thumbnail string
	Tags     []Tag
	Comments []Comment
	Uploader string
	Views int64
	LinkPost bool
	UrlLink string
}

type Comment struct {
	ID       int64
	PageID   int64
	Username sql.NullString
	Content  string
	PostTime time.Time
}

type Tag struct {
	ID       int64
	Name     string
	Selected bool
}

const (
	MAX_UPLOAD_SIZE int64 = 100 << 20 // 100 mb max upload size, ensure nginx server config is set to match/exceed
)

// Render everything but base page/splash, uses template name for page title
func RenderTemplate(w http.ResponseWriter, r *http.Request, template_name string, data interface{}, st *sessions.CookieStore) {
	templ_path := filepath.Join("templates", template_name+".html")

	tmpl, err := template.ParseFiles("templates/base.html", templ_path)
	if err != nil {
		if os.IsNotExist(err) {
			RenderTemplate(w, r, "NotFound", nil, st)
			return
		}
		log.Printf("error parsing templates for '%v' page: %v", template_name, err)
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
		"Title":    template_name,
		"Username": username,
		"Uploader": uploader,
		"Admin":	admin,
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

	requestURI := r.URL.RequestURI()

	data := map[string]interface{}{
		"Path": requestURI,
	}

	err = tmpl.Execute(w, data)
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

func HomePage(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsAuthed(r, st) {
		RenderSplash(w, r)
		return
	}

	selectedTag := r.URL.Query().Get("tag")

	var rows *sql.Rows
	var err error
	if selectedTag == "" {
		query := `
			SELECT id, title, post_time, thumbnail, uploader FROM pages
			ORDER BY post_time DESC
		`
		rows, err = db.Query(query)
		if err != nil {
			log.Printf("Failed to get pages from DB: %v", err)
			return
		}
	} else {
		query := `
			SELECT DISTINCT p.id, p.title, p.post_time, p.thumbnail, p.uploader
			FROM pages p
			JOIN page_tags pt ON p.id = pt.page_id
			JOIN tags t ON pt.tag_id = t.id
			WHERE t.name = ?
			ORDER BY p.post_time DESC
		`

		rows, err = db.Query(query, selectedTag)
		if err != nil {
			log.Printf("failed to access rows with tag '%v': %v", selectedTag, err)
			RenderTemplate(w, r, "NotFound", nil, st)
			return
		}
	}
	defer rows.Close()

	pages := []BlogPage{}

	for rows.Next() {
		var p BlogPage
		err := rows.Scan(&p.ID, &p.Title, &p.PostTime, &p.Thumbnail, &p.Uploader)
		if err != nil {
			log.Printf("Failed to scan row: %v", err)
			return
		}

		p.Tags, err = GetPostTags(p.ID, db)
		if err != nil {
			log.Printf("error getting tags for '%v': %v", p.Title, err)
			return
		}

		pages = append(pages, p)
	}

	// get all tags from DB for tag list
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
		err = tag_rows.Scan(&t.Name)
		if err != nil {
			log.Printf("failed to scan row for tag: %v", err)
			return
		}
		tags = append(tags, t)
	}

	data := struct {
		Pages       []BlogPage
		Tags        []Tag
		SelectedTag string
	}{
		Pages:       pages,
		Tags:        tags,
		SelectedTag: selectedTag,
	}

	RenderTemplate(w, r, "Home", data, st)
}

//
// Uploader page (author page, shows details of uploaders)
//

func UploaderPage(w http.ResponseWriter, r *http.Request, st *sessions.CookieStore) {
	if !users.IsAuthed(r, st) {
		RenderSplash(w, r)
		return
	}

	title := r.URL.Path[len("/uploader/"):]
	RenderTemplate(w, r, title, nil, st)
}

func UploadPage(w http.ResponseWriter, r *http.Request, st *sessions.CookieStore) {
	if !users.IsUploader(r, st) {
		log.Printf("non uploader attempted to access accounts page handler from: %v", r.Host)
		RenderTemplate(w, r, "NotFound", nil, st)
		return
	}
	RenderTemplate(w, r, "Upload", nil, st)
}

// returns true if page already exists
func addPageToDB(db *sql.DB, title string, content string, post_time time.Time, image64 string, thumbnail64 string, tags []string, uploader string, unlisted bool, link_post bool, url_link string) (error, bool) {
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
	result, err := tx.Exec("INSERT INTO pages (title, content, post_time, image, thumbnail, uploader, unlisted, link_post, url_link) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		title, content, post_time, image64, thumbnail64, uploader, unlisted, link_post, url_link)
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

	uploader_name, err := users.GetCurrentUsername(r, st); if err != nil {
		w.Write([]byte("Error getting username from session"))
		return
	}

	content := r.FormValue("description")

	unlisted := r.FormValue("unlisted") == "on"  // if unlisted form is on, set unlisted var to true

	// add links
	link_post := r.FormValue("link_post") == "on" 
	url_link := r.FormValue("url_link")

    post_time_str := r.FormValue("post_time")
    var post_time time.Time
    if post_time_str != "" {
        // Parse the datetime-local value
        post_time, err = time.Parse("2006-01-02T15:04", post_time_str)
        if err != nil {
            w.Write([]byte("Invalid date format"))
            return
        }
    } else {
        post_time = time.Now() // Default to current time if none specified
    }

	file, _, err := r.FormFile("image")
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

	file64 := base64.StdEncoding.EncodeToString(file_bytes)

	// make thumbnail image64
	thumb, _, err := image.Decode(bytes.NewReader(file_bytes)); if err != nil {
		w.Write([]byte("Error decoding image for thumbnail"))
		return
	}

	thumb = imaging.Fill(thumb, 300, 300, imaging.Top, imaging.Lanczos)
	var buf bytes.Buffer
	err = imaging.Encode(&buf, thumb, imaging.JPEG, imaging.JPEGQuality(80))
	if err != nil {
		w.Write([]byte("Error encoding image for thumbnail"))
		return
	}

	small64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	err, exists := addPageToDB(db, title, content, post_time, file64, small64, tags, uploader_name, unlisted, link_post, url_link)
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

func EditPageHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
    if !users.IsUploader(r, st) {
        w.Write([]byte("Unauthorized access"))
        return
    }

    err := r.ParseMultipartForm(MAX_UPLOAD_SIZE)
    if err != nil {
        w.Write([]byte("Invalid request - file may be too large"))
        return
    }

    original_title := r.FormValue("original_title")
    if original_title == "" {
        w.Write([]byte("Missing original title, error on backend"))
        return
    }

	title := r.FormValue("title")
    if title == "" {
        w.Write([]byte("Title cannot be empty"))
        return
    }

    curr_pg, err := getPageFromDB(original_title, db)
    if err != nil {
        w.Write([]byte("Error getting current page from database"))
        return
    }

    requesting_uploader, err := users.GetCurrentUsername(r, st)
    if err != nil {
        w.Write([]byte("Unrecognized user"))
        return
    }

    if curr_pg.Uploader != requesting_uploader && !users.IsAdmin(r, st) {
        w.Write([]byte("Insufficient permissions to edit page"))
        return
    }

    // Start transaction
    tx, err := db.Begin()
    if err != nil {
        w.Write([]byte("Database error"))
        return
    }
    defer tx.Rollback()

	// Get the current page ID
	var pageID int64
	err = tx.QueryRow("SELECT id FROM pages WHERE title = ?", curr_pg.Title).Scan(&pageID)
	if err != nil {
		w.Write([]byte("Error retrieving page ID"))
		return
	}

	post_time_str := r.FormValue("post_time")
    var post_time time.Time
    if post_time_str != "" {
        // Parse the datetime-local value
        post_time, err = time.Parse("2006-01-02T15:04", post_time_str)
        if err != nil {
            w.Write([]byte("Invalid date format"))
            return
        }
    } else {
        post_time = curr_pg.PostTime  // Default to current page time if none specified
    }

	// add links
	link_post := r.FormValue("link_post") == "on" 
	url_link := r.FormValue("url_link")

	// Update the main page content
	updateQuery := `
        UPDATE pages 
        SET content = ?,
            post_time = ?,
            unlisted = ?,
			title = ?,
			link_post = ?,
			url_link = ?
        WHERE id = ?
    `
    _, err = tx.Exec(updateQuery, 
        r.FormValue("description"),
        post_time,
        r.FormValue("unlisted") == "on",
		r.FormValue("title"),
		link_post,
		url_link,
        pageID,
		)
    if err != nil {
        w.Write([]byte("Error updating page content"))
        return
    }


    // Delete existing tags for this page
    _, err = tx.Exec("DELETE FROM page_tags WHERE page_id = ?", pageID)
    if err != nil {
        w.Write([]byte("Error updating tags"))
        return
    }

    // Add new tags
    tags_string := r.FormValue("tags")
    tags := strings.Fields(strings.ReplaceAll(tags_string, ",", " "))
    
    for _, tagName := range tags {
        if tagName == "" {
            continue
        }

        // Insert tag if it doesn't exist and get its ID
        var tagID int64
        err = tx.QueryRow(`
            INSERT INTO tags (name) 
            VALUES (?) 
            ON CONFLICT(name) DO UPDATE SET name=name 
            RETURNING id`, tagName).Scan(&tagID)
        if err != nil {
            w.Write([]byte("Error updating tags"))
            return
        }

        // Link tag to page
        _, err = tx.Exec(`
            INSERT INTO page_tags (page_id, tag_id) 
            VALUES (?, ?)`, pageID, tagID)
        if err != nil {
            w.Write([]byte("Error linking tags"))
            return
        }
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
        // Non-critical error, don't return
    }

	//
	// modify the image, if a new image was given
	//

	file, _, err := r.FormFile("image")
	if err != nil {
		// No image to update, commit transaction
		if err = tx.Commit(); err != nil {
			w.Write([]byte("Error saving changes"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`
			<div class="alert alert-success">
				Page updated successfully! (no image update provided)
			</div>
		`))
		return
	}

	// image exists, update the image

	defer file.Close()

	file_bytes, err := io.ReadAll(file)
	if err != nil {
		return
	}

	file64 := base64.StdEncoding.EncodeToString(file_bytes)

	// make thumbnail image64
	thumb, _, err := image.Decode(bytes.NewReader(file_bytes)); if err != nil {
		w.Write([]byte("Error decoding image for thumbnail"))
		return
	}

	thumb = imaging.Fill(thumb, 300, 300, imaging.Top, imaging.Lanczos)
	var buf bytes.Buffer
	err = imaging.Encode(&buf, thumb, imaging.JPEG, imaging.JPEGQuality(80))
	if err != nil {
		w.Write([]byte("Error encoding image for thumbnail"))
		return
	}

	small64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	// update database

	img_update_query := `
		UPDATE pages 
		SET image = ?,
			thumbnail = ?
		WHERE id = ?
	`
	_, err = tx.Exec(img_update_query, 
		file64,
		small64,
		pageID)
	if err != nil {
		log.Printf("error updating image: %v", err)
		w.Write([]byte("Error updating image"))
		return
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		w.Write([]byte("Error saving changes"))
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`
		<div class="alert alert-success">
			Page and image updated successfully!
		</div>
	`))
}

func EditPage(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsUploader(r, st) {
		log.Printf("non uploader attempted to use edit page: %v", r.Host)
		return
	}

	title := r.URL.Path[len("/edit-page/"):]

	pg, err := getPageFromDB(title, db); if err != nil {
		log.Printf("error getting '%v': %v", title, err)
		return
	}

	tag_string := strings.Join(func() []string {
		names := make([]string, len(pg.Tags))
		for i, tag := range pg.Tags {
			names[i] = tag.Name
		}
		return names
	}(), " ")

	data := map[string]interface{}{
		"Page": pg,
		"TagString": tag_string,
	}

	RenderTemplate(w, r, "Edit Page", data, st)	
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

	w.Header().Set("HX-Redirect", "/")
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

func incrementPageViews(db *sql.DB, pageID int64) error {
    query := `
        UPDATE pages 
        SET views = views + 1 
        WHERE id = ?
    `
    _, err := db.Exec(query, pageID)
    return err
}

func getPageFromDB(title string, db *sql.DB) (*BlogPage, error) {
	query := "SELECT id, title, content, post_time, image, uploader, views, link_post, url_link FROM pages WHERE title = ?"

	row := db.QueryRow(query, title)
	var p BlogPage

	// TODO: update so it gives a different err for it being missing from database vs some other issue
	err := row.Scan(&p.ID, &p.Title, &p.Content, &p.PostTime, &p.Image, &p.Uploader, &p.Views, &p.LinkPost, &p.UrlLink)
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

	w.Header().Set("HX-Refresh", "true")
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

//
// Page navigation buttons, get next page/get previous page with tags
//

func getAdjacentPage(query string, title string, tag string, db *sql.DB) (string, error) {
    var adj_title string
    var err error
    
    if tag != "" {
        err = db.QueryRow(query, title, tag).Scan(&adj_title)
    } else {
        err = db.QueryRow(query, title).Scan(&adj_title)
    }
    
    if err != nil {
        if err == sql.ErrNoRows {
            return "", nil
        }
        log.Printf("Error getting next page %v", err)
        return "", err
    }

    return adj_title, nil
}

func getNextPage(title string, tag string, db *sql.DB) (string, error) {
    var query string

    if tag != "" {
        // Query with tag filter
        query = `
            WITH current_time AS (
                SELECT post_time 
                FROM pages 
                WHERE title = ?
            )
            SELECT p.title 
            FROM pages p
            JOIN page_tags pt ON p.id = pt.page_id
            JOIN tags t ON pt.tag_id = t.id
            WHERE p.post_time > (SELECT post_time FROM current_time)
            AND t.name = ?
            ORDER BY p.post_time ASC
            LIMIT 1
        `
    } else {
        query = `
            SELECT title 
            FROM pages 
            WHERE post_time > (
                SELECT post_time 
                FROM pages 
                WHERE title = ?
            )
            ORDER BY post_time ASC
            LIMIT 1
        `
    }

    return getAdjacentPage(query, title, tag, db)
}

func getPrevPage(title string, tag string, db *sql.DB) (string, error) {

	var query string

    if tag != "" {
        // Query with tag filter
        query = `
            WITH current_time AS (
                SELECT post_time 
                FROM pages 
                WHERE title = ?
            )
            SELECT p.title 
            FROM pages p
            JOIN page_tags pt ON p.id = pt.page_id
            JOIN tags t ON pt.tag_id = t.id
            WHERE p.post_time < (SELECT post_time FROM current_time)
            AND t.name = ?
            ORDER BY p.post_time DESC
            LIMIT 1
        `
    } else {
        query = `
			SELECT title 
			FROM pages 
			WHERE post_time < (
				SELECT post_time 
				FROM pages 
				WHERE title = ?
			)
			ORDER BY post_time DESC
			LIMIT 1
		`
    }

	adj, err := getAdjacentPage(query, title, tag, db)

	return adj, err
}

//
// Page Requests
//

func PageRequest(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !users.IsAuthed(r, st) {
		// log.Println("accessing PageRequest without auth")
		RenderSplash(w, r)
		return
	}

	title := r.URL.Path[len("/page/"):]


	// follow tag is "" if no param in url
 	follow_tag := r.URL.Query().Get("tag")

	p, err := getPageFromDB(title, db)
	if err != nil {
		log.Printf("Error getting generic page '%v' from database: %v", title, err)
		RenderTemplate(w, r, "NotFound", nil, st)
		return
	}

	err = incrementPageViews(db, p.ID)
    if err != nil {
        log.Printf("Error incrementing views for page '%v': %v", title, err)
        // continue anyway
    }

	// Rendering a post page with template (different case than RenderTemplate)

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

	// TODO: add first/last
	// get next and prev page (returns "" if no next/prev page exists)
	next, err := getNextPage(p.Title, follow_tag, db); if err != nil {
		log.Printf("Error getting next page: %v", err)
	}
	prev, err := getPrevPage(p.Title, follow_tag, db); if err != nil {
		log.Printf("Error getting prev page: %v", err)
	}

	content := map[string]interface{}{
		"Title":    	p.Title,
		"Username": 	username,
		"Admin":    	admin,
		"Uploader": 	uploader,
		"NextPage": 	next,
		"PrevPage": 	prev,
		"FollowTag": 	follow_tag,
		"Data":     	p,
	}

	err = tmpl.ExecuteTemplate(w, "base.html", content)
	if err != nil {
		log.Printf("error rendering templates for test page: %v", err)
		return
	}
}
