package main


// SECONDARY TODOs
// TODO: add option to make page unlisted from home page (admins/uploaders only, make an indicator for these posts)
// TODO: make it so pages set to the future aren't sent to users (unless admin/uploader)
// TODO: set up like/heart button
// TODO: add ability to click image to zoom to fit left/right, click again to return to vertical orientation
// TODO: add hover button/highlight to images like in title bar
// TODO: add notification when comments happen
// TODO: check what happens when too large of a tag is used on a page on mobile, there is a chance it might be not
//		 not shown if its too long. If this is an issue, just add a max tag length
// TODO: in page nav bar, home button in the middle going back to the home page with that tag still active 


// TERTIARY TODOs
// TODO: make blog.go into pages.go - consider moving DB stuff to database module (and session module)
// TODO: set up backup schedule
// TODO: search function
// TODO: add blog list page that updates based on blogspot API
// TODO: list comment count on each page on home page

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
	"fmt"

	// externals
	_ "github.com/glebarez/sqlite"
	"github.com/gorilla/sessions"
)

const (
	DatabasePath 	= "database_blog.db"
	ImagePath    	= "images"
	DatabaseVersion	= "1.4"
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

	_, err = db.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		log.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// initializing blog table
	page_query :=
		`
		CREATE TABLE IF NOT EXISTS pages (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
		display_title TEXT NOT NULL,
        content TEXT NOT NULL,
		post_time TIMESTAMP,
        image TEXT NOT NULL,
		thumbnail TEXT NOT NULL,
		uploader TEXT NOT NULL,
		level TEXT NOT NULL DEFAULT 'public',
		likes INTEGER DEFAULT 0,
		hearts INTEGER DEFAULT 0,
		unlisted BOOL DEFAULT 0,
		views INTEGER DEFAULT 0,
		link_post BOOL DEFAULT 0,
		url_link TEXT NOT NULL DEFAULT '404'
    	);`

	_, err = db.Exec(page_query)
	if err != nil {
		log.Fatalf("Failed to add page table to DB: %v", err)
	}

	// init tag table
	tag_query :=
		`
		CREATE TABLE IF NOT EXISTS tags (
		id INTEGER PRIMARY KEY,
		name TEXT UNIQUE NOT NULL
		);
		`

	_, err = db.Exec(tag_query)
	if err != nil {
		log.Fatalf("Failed to add tag table to DB: %v", err)
	}

	// junction tag table
	page_tags_query :=
		`
		CREATE TABLE IF NOT EXISTS page_tags (
		page_id INTEGER NOT NULL,
		tag_id INTEGER NOT NULL,
		FOREIGN KEY (page_id) REFERENCES pages(id) 
			ON DELETE CASCADE 
			ON UPDATE CASCADE,
		FOREIGN KEY (tag_id) REFERENCES tags(id) 
			ON DELETE CASCADE 
			ON UPDATE CASCADE,
		PRIMARY KEY (page_id, tag_id)
		);
		`
	_, err = db.Exec(page_tags_query)
	if err != nil {
		log.Fatalf("Failed to add page tags table to DB: %v", err)
	}

	// initializing comments table, must match Comment struct in blog.go
	comments_query := `
    CREATE TABLE IF NOT EXISTS comments (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        page_id INTEGER NOT NULL,
        username TEXT,  -- NULL for anonymous comments
        content TEXT NOT NULL,
        post_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (page_id) REFERENCES pages(id) 
            ON DELETE CASCADE 
            ON UPDATE CASCADE,
        FOREIGN KEY (username) REFERENCES users(username) 
            ON DELETE SET NULL 
            ON UPDATE CASCADE
    );`
	_, err = db.Exec(comments_query)
	if err != nil {
		log.Fatalf("Failed to add comments table to DB: %v", err)
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
		log.Fatalf("Failed to add users table to DB: %v", err)
	}

	// subscription levels table
	sub_query :=
		`
		CREATE TABLE subscription_levels (
    	id INTEGER PRIMARY KEY,
    	name TEXT UNIQUE NOT NULL
		);`
	
	_, err = db.Exec(sub_query)
	if err != nil {
		log.Fatalf("Failed to add subscriptions table to DB: %v", err)
	}

	sub_junc_query :=
		`
		CREATE TABLE user_subscriptions (
			user_id INTEGER NOT NULL,
			subscription_id INTEGER NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id) 
				ON DELETE CASCADE 
				ON UPDATE CASCADE,
			FOREIGN KEY (subscription_id) REFERENCES subscription_levels(id) 
				ON DELETE CASCADE 
				ON UPDATE CASCADE,
			PRIMARY KEY (user_id, subscription_id)
		);`

	_, err = db.Exec(sub_junc_query)
	if err != nil {
		log.Fatalf("Failed to add subscriptions junction table to DB: %v", err)
	}

	version_query := `
    CREATE TABLE IF NOT EXISTS db_version (
        version TEXT NOT NULL
    );
    INSERT INTO db_version (version) VALUES (?);
    `
    
    _, err = db.Exec(version_query, DatabaseVersion)
    if err != nil {
        log.Fatalf("Failed to add version table to DB: %v", err)
    }

	log.Printf("New database successfully created at (%s)", DatabasePath)

	return true
}

func updateDB_1_1_to_1_2(db *sql.DB) error {
	log.Printf("Attempting to update databse from 1.1 to 1.2")
    var found_version string
    err := db.QueryRow("SELECT version FROM db_version LIMIT 1").Scan(&found_version)
    if err != nil {
        return fmt.Errorf("failed to get database version: %v", err)
    }

    if found_version != "1.1" {
        return fmt.Errorf("wrong database version for migration: expected 1.1, got %v", found_version)
    }

    // Start transaction
    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer tx.Rollback() // Will rollback if we don't commit

    _, err = tx.Exec(`ALTER TABLE pages ADD COLUMN views INTEGER DEFAULT 0;`)
    if err != nil {
        return fmt.Errorf("failed to add views column: %v", err)
    }

    _, err = tx.Exec(`UPDATE db_version SET version = '1.2';`)
    if err != nil {
        return fmt.Errorf("failed to update version number: %v", err)
    }

    err = tx.Commit()
    if err != nil {
        return fmt.Errorf("failed to commit changes: %v", err)
    }

    log.Printf("Successfully migrated database from version 1.1 to 1.2")
    return nil
}

func updateDB_1_2_to_1_3(db *sql.DB) error {
	log.Printf("Attempting to update databse from 1.2 to 1.3")
	var found_version string
    err := db.QueryRow("SELECT version FROM db_version LIMIT 1").Scan(&found_version)
    if err != nil {
        return fmt.Errorf("failed to get database version: %v", err)
    }

    if found_version != "1.2" {
        return fmt.Errorf("wrong database version for migration: expected 1.2, got %v", found_version)
    }

    // Start transaction
    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer tx.Rollback() // Will rollback if we don't commit

    _, err = tx.Exec(`ALTER TABLE pages ADD COLUMN link_post BOOL DEFAULT 0;`)
    if err != nil {
        return fmt.Errorf("failed to add link_post column: %v", err)
    }

	_, err = tx.Exec(`ALTER TABLE pages ADD COLUMN url_link TEXT NOT NULL DEFAULT '404';`)
    if err != nil {
        return fmt.Errorf("failed to add url_link column: %v", err)
    }

    _, err = tx.Exec(`UPDATE db_version SET version = '1.3';`)
    if err != nil {
        return fmt.Errorf("failed to update version number: %v", err)
    }

    err = tx.Commit()
    if err != nil {
        return fmt.Errorf("failed to commit changes: %v", err)
    }

    log.Printf("Successfully migrated database from version 1.2 to 1.3")
    return nil
}

func getCurrentDBVersion(db *sql.DB) (string, error) {
    var version string
    err := db.QueryRow("SELECT version FROM db_version LIMIT 1").Scan(&version)
    return version, err
}

// fatal error if database version doesn't match global var
func check_database_version(db *sql.DB) error {
    currentVersion, err := getCurrentDBVersion(db)
    if err != nil {
        return fmt.Errorf("failed to get database version: %v", err)
    }

    for currentVersion != DatabaseVersion {
        var updateFn func(*sql.DB) error
        var nextVersion string

        switch currentVersion {
        case "1.1":
            updateFn = updateDB_1_1_to_1_2
            nextVersion = "1.2"
        case "1.2":
            updateFn = updateDB_1_2_to_1_3
            nextVersion = "1.3"
        default:
            return fmt.Errorf("unsupported database version '%s' (target: '%s')", currentVersion, DatabaseVersion)
        }

        log.Printf("updating database from version '%s' to '%s'", currentVersion, nextVersion)
        if err := updateFn(db); err != nil {
            return fmt.Errorf("failed to update database from %s to %s: %v", currentVersion, nextVersion, err)
        }

        currentVersion, err = getCurrentDBVersion(db)
        if err != nil {
            return fmt.Errorf("failed to get database version after update: %v", err)
        }
    }

    log.Printf("current database version matches '%s'", DatabaseVersion)
    return nil
}

func main() {

	log_file, err := os.OpenFile("blog.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer log_file.Close()
	mult := io.MultiWriter(os.Stdout, log_file)
	log.SetOutput(mult)

	init_run := initDatabaseIfNone()

	// accessing database, serving it
	db, err := sql.Open("sqlite", DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	check_database_version(db)

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
		Secure:   false, // Set to true if using HTTPS, false if not
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
	// TODO: add root / request that forward to the most recent page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		blog.HomePage(w, r, db, st)
	})
	mux.HandleFunc("/page/", func(w http.ResponseWriter, r *http.Request) {
		blog.PageRequest(w, r, db, st)
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
	mux.HandleFunc("/uploader/", func(w http.ResponseWriter, r *http.Request) {
		blog.UploaderPage(w, r, st)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		blog.RenderTemplate(w, r, "Log In", nil, st)
	})
	mux.HandleFunc("/edit-page/", func (w http.ResponseWriter, r *http.Request) {
		blog.EditPage(w, r, db, st)
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
	mux.HandleFunc("/modify-page", func(w http.ResponseWriter, r *http.Request) {
		blog.EditPageHandler(w, r, db, st)
	})
	mux.HandleFunc("/request-account", func(w http.ResponseWriter, r *http.Request) {
		users.NewUserAccountRequestHandler(w, r, db, st)
	})
	mux.HandleFunc("/request-login", func(w http.ResponseWriter, r *http.Request) {
		users.RequestLogin(w, r, db, st)
	})
	mux.HandleFunc("/request-logout", func(w http.ResponseWriter, r *http.Request) {
		users.RequestLogout(w, r, db, st)
	})
	mux.HandleFunc("/request-authenticate/", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/add-comment", func(w http.ResponseWriter, r *http.Request) {
		blog.AddCommentHandler(w, r, db, st)
	})

	// serve static files (deps/images)
	fileServer := http.FileServer(http.Dir("."))
	mux.Handle("/dep/", fileServer)
	mux.Handle("/images/", fileServer)
	mux.Handle("/games/", fileServer)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server{
		Addr:    ":8080",
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shut down: ", err)
	}

	log.Println("Server exiting")
}
