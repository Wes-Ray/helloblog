package users

import (

	// golang
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	// externals
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

// match users table in sql db
type User struct {
	Username 		string
	Email 			string
	Admin			bool
	Uploader 		bool  
	Created 		time.Time
	LastLogin 		time.Time
}

func (u User) String() string {
	return fmt.Sprintf(
	`
Username: %s
Email: %s
Admin: %v
Uploader: %v  
Created: %v
LastLogin: %v
	`, u.Username, u.Email, u.Admin, u.Uploader, u.Created, u.LastLogin)
}

// consts for session values
const (
	sesADMIN 			string = "admin"
	sesUPLOADER 		string = "uploader"
	sesAUTHENTICATED 	string = "authenticated"
	sesUSERNAME			string = "username"
)

func getUserFromDB(username string, db *sql.DB) (User, error) {
    query := "SELECT username, email, admin, uploader, created, last_login FROM users WHERE username = ?"
    var u User
    err := db.QueryRow(query, username).Scan(&u.Username, &u.Email, &u.Admin, &u.Uploader, &u.Created, &u.LastLogin)
    if err != nil {
        return User{}, fmt.Errorf("failed to retrieve user '%v' from DB: %v", username, err)
    }
    return u, nil
}

func HasSubscriptionLevel(db *sql.DB, username string, level string) (bool, error) {
    query := `
        SELECT EXISTS (
            SELECT 1 FROM users u
            JOIN user_subscriptions us ON u.id = us.user_id
            JOIN subscription_levels sl ON us.subscription_id = sl.id
            WHERE u.username = ? AND sl.name = ?
        )
    `
    var exists bool
    err := db.QueryRow(query, username, level).Scan(&exists)
    return exists, err
}

func IsAdmin(r *http.Request, st *sessions.CookieStore) bool {
    session, err := st.Get(r, "session")
    if err != nil {
        log.Printf("error getting session with isAdmin: %v", err)
        return false
    }

    // Check if authenticated first
    auth, ok := session.Values[sesAUTHENTICATED]
    if !ok || auth == nil {
        return false
    }
    authBool, ok := auth.(bool)
    if !ok || !authBool {
        return false
    }

    // Then check admin status
    admin, ok := session.Values[sesADMIN]
    if !ok || admin == nil {
        return false
    }
    adminBool, ok := admin.(bool)
    if !ok {
        return false
    }
    return adminBool
}

func SetUserAdmin(db *sql.DB, username string, admin_status bool) error {
	query := `
		UPDATE users
		SET admin = ?
		WHERE username = ?;
	`

	res, err := db.Exec(query, admin_status, username); if err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected(); if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("user %v not found", username)
	}
	return nil
}

func ToggleAdmin(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !IsAdmin(r, st) {
		log.Printf("non admin attempted to access accounts page handler from: %v", r.Host)
		http.Error(w, "Admins only", http.StatusForbidden)
		return
	}

	err := r.ParseForm(); if err != nil {
		log.Printf("Error parsing form: %v", err)
        http.Error(w, "Invalid request", http.StatusBadRequest)
	}
	
    username := r.Form.Get("username")

    if username == "" {
        log.Printf("Error: Missing required fields in toggle admin request")
        http.Error(w, "Invalid request: All fields are required", http.StatusBadRequest)
        return
    }

	user, err := getUserFromDB(username, db); if err != nil {
		log.Printf("invalid request for '%v' to be changed admin status: %v", username, err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
        return
	}

	err = SetUserAdmin(db, user.Username, !user.Admin); if err != nil {
		log.Printf("Failed to set user admin status: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`
		<div class="alert alert-success">
			Updated account status.
		</div>
		`))
}

func InitAdmin(db *sql.DB) {

	fmt.Println("Initializing admin account")

	admin_username := string(os.Getenv("ADMIN_USERNAME"))
	admin_password := string(os.Getenv("ADMIN_PASSWORD"))
	if len(admin_password) == 0 || len(admin_username) == 0 {
		log.Fatal("ADMIN_PASSWORD and ADMIN_USERNAME environment variable must be set")
	}
	
	// adding admin user/password
	err := AddLoginToDB(db, admin_username, admin_password, "")
	if err != nil {
		log.Fatalf("Failed to created admin user: %v", err)
	}
	err = SetUserAdmin(db, admin_username, true); if err != nil {
		log.Fatalf("Failed to give admin to initial admin: %v", err)
	}
	log.Println("Created super admin")
	PrintUser(db, admin_username)
}

func IsUploader(r *http.Request, st *sessions.CookieStore) bool {
    session, err := st.Get(r, "session")
    if err != nil {
        log.Printf("error getting session with IsUploader: %v", err)
        return false
    }

    // Check if authenticated first
    auth, ok := session.Values[sesAUTHENTICATED]
    if !ok || auth == nil {
        return false
    }
    authBool, ok := auth.(bool)
    if !ok || !authBool {
        return false
    }

    // Then check uploader status
    upl, ok := session.Values[sesUPLOADER]
    if !ok || upl == nil {
        return false
    }
    uplBool, ok := upl.(bool)
    if !ok {
        return false
    }
    return uplBool
}


func SetUserUploader(db *sql.DB, username string, uploader_status bool) error {
	query := `
		UPDATE users
		SET uploader = ?
		WHERE username = ?;
	`

	res, err := db.Exec(query, uploader_status, username); if err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected(); if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("user %v not found", username)
	}
	return nil
}

func ToggleUploader(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !IsAdmin(r, st) {
		log.Printf("non admin attempted to access accounts page handler from: %v", r.Host)
		http.Error(w, "Admins only", http.StatusForbidden)
		return
	}

	err := r.ParseForm(); if err != nil {
		log.Printf("Error parsing form: %v", err)
        http.Error(w, "Invalid request", http.StatusBadRequest)
	}
	
    username := r.Form.Get("username")

    if username == "" {
        log.Printf("Error: Missing required fields in toggle uploader request")
        http.Error(w, "Invalid request: All fields are required", http.StatusBadRequest)
        return
    }

	user, err := getUserFromDB(username, db); if err != nil {
		log.Printf("invalid request for '%v' to be changed uploader status: %v", username, err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
        return
	}

	err = SetUserUploader(db, user.Username, !user.Uploader); if err != nil {
		log.Printf("Failed to set user uploader status: %v", err)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`
		<div class="alert alert-success">
			Updated account status.
		</div>
		`))
}

func NewUserPage(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	tmpl, err := template.ParseFiles("templates/new-user.html"); if err != nil {
		log.Printf("error parsing template: %v", err)
		return
	}

	err = tmpl.Execute(w, nil); if err != nil {
		log.Printf("error rendering new user template: %v", err)
		return
	}
}

func checkLogin(db *sql.DB, username string, pass string) error {
	query := "SELECT hash FROM users WHERE username = ?"
	row := db.QueryRow(query, username)

	var db_hash []byte
	err := row.Scan(&db_hash); if err != nil {
		return err
	}

	err = bcrypt.CompareHashAndPassword(db_hash, []byte(pass))
	return err
}

func NewUserAccountRequestHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	err := r.ParseForm(); if err != nil {
		log.Printf("Error parsing form: %v", err)
        http.Error(w, "Invalid request", http.StatusBadRequest)
	}
	
    username := r.Form.Get("username")
    email := r.Form.Get("email")
    password := r.Form.Get("password")
	password2 := r.Form.Get("password2")

    if username == "" || password == "" || password2 == "" {
        log.Printf("Error: Missing required fields in user account request")
        http.Error(w, "Invalid request: All fields are required", http.StatusBadRequest)
        return
    }
	if password != password2 {
		w.Write([]byte(`
		<div class="alert alert-success">
			Passwords do not match.
		</div>
		`))
		return
	}
	
	// add account to DB
	err = AddLoginToDB(db, username, password, email)
	if err != nil {
		w.WriteHeader(http.StatusConflict)
		errstr := fmt.Sprintf(`
		<div class="alert alert-warning">
			%v
		</div>
		`, err)
		w.Write([]byte(errstr))
		return
	}

	// Create session for the new user
	user, err := getUserFromDB(username, db)
	if err != nil {
		log.Printf("failed to access user to create session: %v", err)
		return
	}

	session, err := st.Get(r, "session")
	if err != nil {
		log.Printf("failed to create session: %v", err)
		return
	}

	session.Values[sesAUTHENTICATED] = true
	session.Values[sesUPLOADER] = user.Uploader
	session.Values[sesADMIN] = user.Admin
	session.Values[sesUSERNAME] = username
	session.Save(r, w)

	// Send back success message 
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`
		<div class="alert alert-success">
			Account creation successful, leave a comment! ❤️
		</div>
		`))
}

func RequestLogin(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	err := r.ParseForm(); if err != nil {
		log.Printf("Error parsing form: %v", err)
        http.Error(w, "Invalid request", http.StatusBadRequest)
	}
	
    username := r.Form.Get("username")
    password := r.Form.Get("password")

    if username == "" || password == "" {
        log.Printf("Error: Missing required fields in user account request")
        http.Error(w, "Invalid request: All fields are required", http.StatusBadRequest)
        return
    }

	err = checkLogin(db, username, password); if err != nil {
		log.Printf("Failed login: %v", err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`
			<div class="alert alert-warning">
				Invalid username or password. Please try again.
			</div>
			`))
		return
	}

	// successful login
	// update login time
	query := `
		UPDATE users
		SET last_login = CURRENT_TIMESTAMP
		WHERE username = ?;
		`
	_, err = db.Exec(query, username); if err != nil {
		log.Printf("failed to update last login for '%v': %v", username, err)
	}

	// create session for auth'd user with rights
	user, err := getUserFromDB(username, db)
	if err != nil {
		log.Printf("failed to access user to create session: %v", err)
		return
	}
	log.Printf("user: %v", user)

	session, err := st.Get(r, "session"); if err != nil {
		log.Printf("failed to create session: %v", err)
		return
	}
	session.Values[sesAUTHENTICATED] = true
	session.Values[sesUPLOADER] = user.Uploader
	session.Values[sesADMIN] = user.Admin
	session.Values[sesUSERNAME] = username
	session.Save(r, w)

	w.Header().Set("HX-Refresh", "true")
}

func RequestAuthentication(w http.ResponseWriter, r *http.Request, st *sessions.CookieStore) {

    session, err := st.Get(r, "session"); if err != nil {
        log.Printf("failed to create get session on auth request: %v", err)
        return
    }

    session.Values[sesAUTHENTICATED] = true

    err = session.Save(r, w)
    if err != nil {
        log.Printf("failed to save session: %v", err)
        return
    }

	// requested_path := r.URL.Path[len("/request-authenticate"):]
	requested_path := r.URL.RequestURI()[len("/request-authenticate"):]
	if requested_path == "" {
		requested_path = "/"
	}

    w.Header().Set("HX-Redirect", requested_path)
}

func RequestLogout(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	session, err := st.Get(r, "session"); if err != nil {
		log.Printf("Error getting session: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	session.Values = make(map[interface{}]interface{})

	err = session.Save(r, w); if err != nil {
		log.Printf("Error saving session: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Refresh", "true")
}

func GetSessionString(r *http.Request, st *sessions.CookieStore) string {
	session, err := st.Get(r, "session"); if err != nil {
		log.Printf("error getting session with getSessionString: %v", err)
		return "error getting session"
	}
	admin := session.Values[sesADMIN]
	uploader := session.Values[sesUPLOADER]
	authd := session.Values[sesAUTHENTICATED]

	ret := fmt.Sprintf("ADMIN: %v UPLOADER: %v AUTH'D: %v", admin, uploader, authd)
	return ret
}

func IsAuthed(r *http.Request, st *sessions.CookieStore) bool {
    session, err := st.Get(r, "session")
    if err != nil {
        log.Printf("error getting session with IsAuthed: %v", err)
        return false
    }

    auth, ok := session.Values[sesAUTHENTICATED]
    if !ok || auth == nil {
        return false
    }
    authBool, ok := auth.(bool)
    if !ok || !authBool {
        return false
    }
    return true
}

func GetCurrentUsername(r *http.Request, st *sessions.CookieStore) (string, error) {
    session, err := st.Get(r, "session")
    if err != nil {
        log.Printf("error getting session in GetCurrentUsername: %v", err)
        return "", err
    }

    username, ok := session.Values[sesUSERNAME]
    if !ok || username == nil {
        return "", fmt.Errorf("username not found in session")
    }
    
    usernameStr, ok := username.(string)
    if !ok {
        return "", fmt.Errorf("username in session is not a string")
    }

    return usernameStr, nil
}

func PrintUser(db *sql.DB, username string) {
	u, err := getUserFromDB(username, db); if err != nil {
		log.Printf("Failed to retrieve user: %v", err)
	}
	log.Printf("user: %v", u)
}

func AddLoginToDB(db *sql.DB, username string, pass string, email string) error {
	var exists bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM USERS WHERE username = ?)", username).Scan(&exists)
	if err != nil {
		log.Printf("Failed to query database: %v", err)
		return fmt.Errorf("failed to query database")
	}
	if exists {
		return fmt.Errorf("'%s' user already exists", username)
	}

	// note: this automatically salts the password
	hashpass, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	stmt, err := db.Prepare("INSERT INTO users (username, email, hash) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(username, email, hashpass); if err != nil {
		return err
	}

	// set creation time
	query := `
		UPDATE users
		SET created = CURRENT_TIMESTAMP
		WHERE username = ?;
	`
	_, err = db.Exec(query, username); if err != nil {
		log.Printf("failed to create account creation time for '%v': %v", username, err)
	}

	log.Printf("added '%v' to DB", username)
	return nil;
}

func DeleteUserHandler(w http.ResponseWriter, r *http.Request, db *sql.DB, st *sessions.CookieStore) {
	if !IsAdmin(r, st) {
		log.Printf("only admins can access this")
		http.Error(w, "Admins only", http.StatusForbidden)
		return
	}

	err := r.ParseForm()
	if err != nil {
		log.Printf("Error parsing form: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	username := r.Form.Get("username")
	if username == "" {
		log.Printf("couldn't get username when deleting")
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	stmt, err := db.Prepare("DELETE FROM users WHERE username = ? AND admin = 0")
	if err != nil {
		log.Printf("error deleting %v from db", username)
		return
	}
	defer stmt.Close()

	result, err := stmt.Exec(username)
	if err != nil {
		log.Printf("failed to execute delete for %v: %v", username, err)
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
		log.Printf("user '%s' not deleted, either an admin or doesn't exist", username)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

    w.Header().Set("HX-Refresh", "true")
    w.WriteHeader(http.StatusOK)
}

func GetUsers(db *sql.DB) ([]User, error) {
	// TODO: add filter for only admins or make admins appear at top
	// TODO: sort alphabetical
	rows, err := db.Query("SELECT username, email, admin, uploader, created, last_login FROM users"); if err != nil {
		return nil, fmt.Errorf("failed to query db for accounts: %v", err)
	}

	users := []User{}
	for rows.Next() {
		var u User
		err := rows.Scan(&u.Username, &u.Email, &u.Admin, &u.Uploader, &u.Created, &u.LastLogin); if err != nil {
			return nil, fmt.Errorf("failed to scan db for accounts: %v", err)
		}

		users = append(users, u)
		// log.Printf("user: %v", u)
	}

	return users, nil
}
