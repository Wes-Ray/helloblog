package main

// PRIMARY TODOs

// TODO: make upload status/progress (upload seems to be broken on https with big files)
// TODO: add login failure status (make it so you can still login, make popup on fail)
// TODO: make it so admin user can't be deleted
// TODO: add comments (logged in and anonymous)
// TODO: tags and navigation
// TODO: make page not found page for StatusNotFound
// TODO: set up like/heart button
// TODO: make blog.go into pages.go - consider moving DB stuff to database module (and session module)
// TODO: review all isAdmin and IsUploader checks to make sure they print who is attempting to access
//		 and redirect to custom 404 page instead of forbidden
// TODO: add check that name is not in page name/htmx indexes/maybe prepend all pages with /p or something
// TODO: make sure all session conversions check for nil before converting to bool, etc
// TODO: add modify item/page
// TODO: add author to page
// TODO: add option to make page unlisted from index page (direct link only)
// TODO: set up firewall
// TODO: set up domain name in session store (see startup TODO)
// TODO: set up backup schedule
// TODO: auto login when creating account
// TODO: add links page
// TODO: client-side hashing of password

// Project Structure
// blog: manage blog pages (upload page, view page, edit page, etc)
// users: manage users/user log-in etc

import (
	// internal
	"blog/internal/blog"
	"blog/internal/users"
	"context"
	"io"
	"os/signal"
	"syscall"
	"time"

	// golang
	"database/sql"
	"log"
	"net/http"
	"os"

	// externals
	_ "github.com/glebarez/sqlite"
	"github.com/gorilla/sessions"
)

const (
	DatabasePath = "database_blog.db"
	PagesPath    = "pages"
	ImagePath    = "images"
)

func initDatabaseIfNone() bool {

	if _, err := os.Stat(DatabasePath); err == nil {
		log.Printf("Using existing database '%s', delete if you want to init a new database.", DatabasePath)
		return false
	}
	log.Printf("No database found at '%s'. Initializing database...", DatabasePath)
	db, err := sql.Open("sqlite", DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}

	// initializing blog table
	page_query :=
		`
		CREATE TABLE IF NOT EXISTS blog (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
        content TEXT NOT NULL,
		post_time TIMESTAMP,
        image TEXT
    	);`

	_, err = db.Exec(page_query)
	if err != nil {
		log.Fatalf("Failed to add table to DB: %v", err)
	}

	// initializing user/pass table, match User struct in users
	user_query :=
		`
		CREATE TABLE IF NOT EXISTS users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		email TEXT NOT NULL,
		hash TEXT NOT NULL,
		admin BOOL NOT NULL DEFAULT 0,
		uploader BOOL NOT NULL DEFAULT 0,
		created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_login DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`

	_, err = db.Exec(user_query)
	if err != nil {
		log.Fatalf("Failed to add table to DB: %v", err)
	}

	log.Printf("New database successfully created at (%s)", DatabasePath)

	return true
}

func main() {

	log_file, err := os.OpenFile("blog.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer log_file.Close()
	mult := io.MultiWriter(os.Stdout, log_file)
	log.SetOutput(mult)

	// TODO: grab admin username/pass from secure store
	init_run := initDatabaseIfNone()

	// accessing database, serving it
	db, err := sql.Open("sqlite", DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// TODO: check if necessary tables exist before continuing

	// setup session store
	key := []byte(os.Getenv("SESSION_KEY"))
	if len(key) == 0 {
		log.Fatal("SESSION_KEY environment variable must be set")
	}
	st := sessions.NewCookieStore(key)

	// Configure session options
	st.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   false,  // Set to true if using HTTPS, false if not
		SameSite: http.SameSiteStrictMode,
	}
	// TODO: update to domain name (as const)
    // st.Options.Domain = "domain.com"

	if init_run {
		users.InitAdmin(db)
	}
	

	// server loop
	log.Println("Starting web server")

	mux := http.NewServeMux()

	//
	// Pages
	//
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		blog.GetRequest(w, r, db, st)
	})
	mux.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		blog.ListPages(w, r, db, st)
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		blog.UploadPage(w, r, st)
	})
	mux.HandleFunc("/sign-up", func(w http.ResponseWriter, r *http.Request) {
		blog.RenderTemplate(w, r, "Sign Up", nil, st)
	})
	mux.HandleFunc("/user-management", func(w http.ResponseWriter, r *http.Request) {
		blog.AccountsPageHandler(w, r, db, st)
	})
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		blog.TestPage(w, r, db, st)
	})

	//
	// Functions (htmx requests etc)
	//
	mux.HandleFunc("/upload-page", func(w http.ResponseWriter, r *http.Request) {
		blog.UploadHandler(w, r, db, st)
	})
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		blog.DeletePageHandler(w, r, db, st)
	})
	mux.HandleFunc("/request-account", func(w http.ResponseWriter, r *http.Request) {
		users.NewUserAccountRequestHandler(w, r, db)
	})
	mux.HandleFunc("/request-login", func(w http.ResponseWriter, r *http.Request) {
		users.RequestLogin(w, r, db, st)
	})
	mux.HandleFunc("/request-logout", func(w http.ResponseWriter, r *http.Request) {
		users.RequestLogout(w, r, db, st)
	})
	mux.HandleFunc("/request-authenticate", func(w http.ResponseWriter, r *http.Request) {
		users.RequestAuthentication(w, r, st)
	})
	mux.HandleFunc("/delete-user", func(w http.ResponseWriter, r *http.Request) {
		users.DeleteUserHandler(w, r, db, st)
	})
	mux.HandleFunc("/toggle-admin", func(w http.ResponseWriter, r *http.Request) {
		users.ToggleAdmin(w, r, db, st)
	})
	mux.HandleFunc("/toggle-uploader", func(w http.ResponseWriter, r *http.Request) {
		users.ToggleUploader(w, r, db, st)
	})

	// serve static files (deps/images)
	fileServer := http.FileServer(http.Dir("."))
	mux.Handle("/dep/", fileServer)
	mux.Handle("/images/", fileServer)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server {
		Addr: ":8080",
		Handler: mux,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// timeout set here
	ctx, cancel := context.WithTimeout(context.Background(), 2 * time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shut down: ", err)
	}

	log.Println("Server exiting")
}