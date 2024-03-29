package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
)

var (
	publicDir string
	fs        http.Handler
)

type User struct {
	ID        string    `db:"id" json:"id"`
	Email     string    `db:"email" json:"email"`
	Nickname  string    `db:"nickname" json:"nickname"`
	Staff     bool      `db:"staff" json:"staff"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

type Schedule struct {
	ID           string         `db:"id" json:"id"`
	Title        string         `db:"title" json:"title"`
	Capacity     int            `db:"capacity" json:"capacity"`
	Reserved     int            `db:"reserved" json:"reserved"`
	Reservations []*Reservation `db:"reservations" json:"reservations"`
	CreatedAt    time.Time      `db:"created_at" json:"created_at"`
}

type Reservation struct {
	ID         string    `db:"id" json:"id"`
	ScheduleID string    `db:"schedule_id" json:"schedule_id"`
	UserID     string    `db:"user_id" json:"user_id"`
	User       *User     `db:"user" json:"user"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
}

type Cache[K comparable, V any] struct {
	sync.RWMutex
	m map[K]V
}

func (c *Cache[K, V]) Get(key K) (value V, ok bool) {
	c.RLock()
	value, ok = c.m[key]
	c.RUnlock()
	return
}

func (c *Cache[K, V]) Set(key K, value V) {
	c.Lock()
	c.m[key] = value
	c.Unlock()
}

var uCache = Cache[string, User]{m: map[string]User{}}

type reserved struct {
	isReserved  bool
	capacity    int
	reservation int
}

var rCache = Cache[string, reserved]{m: map[string]reserved{}}

func getCurrentUser(r *http.Request) *User {
	uidCookie, err := r.Cookie("user_id")
	if err != nil || uidCookie == nil {
		return nil
	}

	user, ok := uCache.Get(uidCookie.Value)
	if ok {
		return &user
	}

	row := db.QueryRowxContext(r.Context(), "SELECT * FROM `users` WHERE `id` = ? LIMIT 1", uidCookie.Value)
	user = User{}
	if err := row.StructScan(&user); err != nil {
		return nil
	}
	uCache.Set(user.ID, user)
	return &user
}

func requiredLogin(w http.ResponseWriter, r *http.Request) bool {
	if getCurrentUser(r) != nil {
		return true
	}
	sendErrorJSON(w, fmt.Errorf("login required"), 401)
	return false
}

func requiredStaffLogin(w http.ResponseWriter, r *http.Request) bool {
	currentUser := getCurrentUser(r)
	if currentUser != nil && currentUser.Staff {
		return true
	}
	sendErrorJSON(w, fmt.Errorf("login required"), 401)
	return false
}

func getReservations(r *http.Request, s *Schedule) error {
	rows, err := db.QueryxContext(r.Context(), "SELECT * FROM `reservations` WHERE `schedule_id` = ?", s.ID)
	if err != nil {
		return err
	}

	defer rows.Close()

	reserved := 0
	s.Reservations = []*Reservation{}
	for rows.Next() {
		reservation := &Reservation{}
		if err := rows.StructScan(reservation); err != nil {
			return err
		}
		reservation.User = getUser(r, reservation.UserID)

		s.Reservations = append(s.Reservations, reservation)
		reserved++
	}
	s.Reserved = reserved

	return nil
}

func getReservationsCount(r *http.Request, s *Schedule) error {
	rows, err := db.QueryxContext(r.Context(), "SELECT * FROM `reservations` WHERE `schedule_id` = ?", s.ID)
	if err != nil {
		return err
	}

	defer rows.Close()

	reserved := 0
	for rows.Next() {
		reserved++
	}
	s.Reserved = reserved

	return nil
}

func getUser(r *http.Request, id string) *User {
	user, ok := uCache.Get(id)
	if !ok {
		user = User{}
		if err := db.QueryRowxContext(r.Context(), "SELECT * FROM `users` WHERE `id` = ? LIMIT 1", id).StructScan(&user); err != nil {
			return nil
		}
		uCache.Set(user.ID, user)
	}

	currentUser := getCurrentUser(r)
	if currentUser != nil && !currentUser.Staff {
		user.Email = ""
	}
	return &user
}

func parseForm(r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return r.ParseForm()
	} else {
		return r.ParseMultipartForm(32 << 20)
	}
}

func serveMux() http.Handler {
	router := mux.NewRouter()

	router.HandleFunc("/initialize", initializeHandler).Methods("POST")
	router.HandleFunc("/api/session", sessionHandler).Methods("GET")
	router.HandleFunc("/api/signup", signupHandler).Methods("POST")
	router.HandleFunc("/api/login", loginHandler).Methods("POST")
	router.HandleFunc("/api/schedules", createScheduleHandler).Methods("POST")
	router.HandleFunc("/api/reservations", createReservationHandler).Methods("POST")
	router.HandleFunc("/api/schedules", schedulesHandler).Methods("GET")
	router.HandleFunc("/api/schedules/{id}", scheduleHandler).Methods("GET")

	dir, err := filepath.Abs(filepath.Join(filepath.Dir(os.Args[0]), "..", "public"))
	if err != nil {
		log.Fatal(err)
	}
	publicDir = dir
	fs = http.FileServer(http.Dir(publicDir))

	router.PathPrefix("/").HandlerFunc(htmlHandler)

	// return logger(router)
	return router
}

func logger(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		before := time.Now()
		handler.ServeHTTP(w, r)
		after := time.Now()
		duration := after.Sub(before)
		log.Printf("%s % 4s %s (%s)", r.RemoteAddr, r.Method, r.URL.Path, duration)
	})
}

func sendJSON(w http.ResponseWriter, data interface{}, statusCode int) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	enc := json.NewEncoder(w)
	return enc.Encode(data)
}

func sendErrorJSON(w http.ResponseWriter, err error, statusCode int) error {
	log.Printf("ERROR: %+v", err)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	enc := json.NewEncoder(w)
	return enc.Encode(map[string]string{"error": err.Error()})
}

type initializeResponse struct {
	Language string `json:"language"`
}

func initializeHandler(w http.ResponseWriter, r *http.Request) {
	err := transaction(r.Context(), &sql.TxOptions{}, func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, "TRUNCATE `reservations`"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "TRUNCATE `schedules`"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "TRUNCATE `users`"); err != nil {
			return err
		}

		uCache = Cache[string, User]{m: map[string]User{}}
		rCache = Cache[string, reserved]{m: map[string]reserved{}}

		id := generateID(tx, "users")
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO `users` (`id`, `email`, `nickname`, `staff`, `created_at`) VALUES (?, ?, ?, true, NOW(6))",
			id,
			"isucon2021_prior@isucon.net",
			"isucon",
		); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		sendErrorJSON(w, err, 500)
	} else {
		sendJSON(w, initializeResponse{Language: "golang"}, 200)
	}
}

func sessionHandler(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, getCurrentUser(r), 200)
}

func signupHandler(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(r); err != nil {
		sendErrorJSON(w, err, 500)
		return
	}

	user := &User{}

	err := transaction(r.Context(), &sql.TxOptions{}, func(ctx context.Context, tx *sqlx.Tx) error {
		email := r.FormValue("email")
		nickname := r.FormValue("nickname")
		id := generateID(tx, "users")
		now := time.Now()

		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO `users` (`id`, `email`, `nickname`, `created_at`) VALUES (?, ?, ?, ?)",
			id, email, nickname, now,
		); err != nil {
			return err
		}
		user.ID = id
		user.Email = email
		user.Nickname = nickname
		user.CreatedAt = now

		return nil
	})
	uCache.Set(user.ID, *user)

	if err != nil {
		sendErrorJSON(w, err, 500)
	} else {
		sendJSON(w, user, 200)
	}
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(r); err != nil {
		sendErrorJSON(w, err, 500)
		return
	}

	email := r.PostFormValue("email")
	user := &User{}

	if err := db.QueryRowxContext(
		r.Context(),
		"SELECT * FROM `users` WHERE `email` = ? LIMIT 1",
		email,
	).StructScan(user); err != nil {
		sendErrorJSON(w, err, 403)
		return
	}
	cookie := &http.Cookie{
		Name:     "user_id",
		Value:    user.ID,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	}
	http.SetCookie(w, cookie)

	sendJSON(w, user, 200)
}

func createScheduleHandler(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(r); err != nil {
		sendErrorJSON(w, err, 500)
		return
	}

	if !requiredStaffLogin(w, r) {
		return
	}

	schedule := &Schedule{}
	err := transaction(r.Context(), &sql.TxOptions{}, func(ctx context.Context, tx *sqlx.Tx) error {
		id := generateID(tx, "schedules")
		title := r.PostFormValue("title")
		capacity, _ := strconv.Atoi(r.PostFormValue("capacity"))
		now := time.Now()

		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO `schedules` (`id`, `title`, `capacity`, `created_at`) VALUES (?, ?, ?, ?)",
			id, title, capacity, now,
		); err != nil {
			return err
		}
		schedule.ID = id
		schedule.Title = title
		schedule.Capacity = capacity
		schedule.CreatedAt = now

		return nil
	})

	if err != nil {
		sendErrorJSON(w, err, 500)
	} else {
		sendJSON(w, schedule, 200)
	}
}

func createReservationHandler(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(r); err != nil {
		sendErrorJSON(w, err, 500)
		return
	}

	if !requiredLogin(w, r) {
		return
	}

	reservation := &Reservation{}
	err := transaction(r.Context(), &sql.TxOptions{}, func(ctx context.Context, tx *sqlx.Tx) error {
		id := generateID(tx, "schedules")
		scheduleID := r.PostFormValue("schedule_id")
		userID := getCurrentUser(r).ID

		capacity := 0
		tx.QueryRowContext(ctx, "SELECT capacity FROM `schedules` WHERE `id` = ? LIMIT 1 FOR UPDATE", scheduleID).Scan(&capacity)
		if capacity <= 0 {
			return sendErrorJSON(w, fmt.Errorf("schedule not found"), 403)
		}

		if _, ok := uCache.Get(userID); !ok {
			return sendErrorJSON(w, fmt.Errorf("user not found"), 403)
		}

		found := 0
		tx.QueryRowContext(ctx, "SELECT 1 FROM `reservations` WHERE `schedule_id` = ? AND `user_id` = ? LIMIT 1", scheduleID, userID).Scan(&found)
		if found == 1 {
			return sendErrorJSON(w, fmt.Errorf("already taken"), 403)
		}

		// capacity := 0
		// if err := tx.QueryRowContext(ctx, "SELECT `capacity` FROM `schedules` WHERE `id` = ? LIMIT 1", scheduleID).Scan(&capacity); err != nil {
		// 	return sendErrorJSON(w, err, 500)
		// }

		// rows, err := tx.QueryContext(ctx, "SELECT * FROM `reservations` WHERE `schedule_id` = ?", scheduleID)
		// if err != nil && err != sql.ErrNoRows {
		// 	return sendErrorJSON(w, err, 500)
		// }
		// reserved := 0
		// for rows.Next() {
		// 	reserved++
		// }

		// if reserved >= capacity {
		// 	return sendErrorJSON(w, fmt.Errorf("capacity is already full"), 403)
		// }

		res, ok := rCache.Get(scheduleID)
		if !ok {
			res = reserved{
				isReserved:  false,
				capacity:    capacity,
				reservation: 0,
			}
			rCache.Set(scheduleID, res)
		}
		if res.isReserved {
			return sendErrorJSON(w, fmt.Errorf("capacity is already full"), 403)
		}

		now := time.Now()
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO `reservations` (`id`, `schedule_id`, `user_id`, `created_at`) VALUES (?, ?, ?, ?)",
			id, scheduleID, userID, now,
		); err != nil {
			return err
		}

		reservation.ID = id
		reservation.ScheduleID = scheduleID
		reservation.UserID = userID
		reservation.CreatedAt = now

		res.reservation++
		res.isReserved = res.capacity <= res.reservation
		rCache.Set(scheduleID, res)

		return sendJSON(w, reservation, 200)
	})
	if err != nil {
		sendErrorJSON(w, err, 500)
	}
}

func schedulesHandler(w http.ResponseWriter, r *http.Request) {
	schedules := []*Schedule{}
	rows, err := db.QueryxContext(r.Context(),
		"SELECT schedules.*, COUNT(reservations.id) as reserved FROM schedules "+
			"LEFT OUTER JOIN reservations ON reservations.schedule_id = schedules.id "+
			"GROUP BY schedules.id ORDER BY schedules.id DESC")
	if err != nil {
		sendErrorJSON(w, err, 500)
		return
	}

	reserved := false
	if r.URL.Query().Get("reserved") == "1" && requiredStaffLogin(w, r) {
		reserved = true
	}

	for rows.Next() {
		schedule := &Schedule{}
		if err := rows.StructScan(schedule); err != nil {
			sendErrorJSON(w, err, 500)
			return
		}
		// if err := getReservationsCount(r, schedule); err != nil {
		// 	sendErrorJSON(w, err, 500)
		// 	return
		// }
		if reserved || schedule.Reserved < schedule.Capacity {
			schedules = append(schedules, schedule)
		}
	}

	sendJSON(w, schedules, 200)
}

func scheduleHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	schedule := &Schedule{}
	if err := db.QueryRowxContext(r.Context(), "SELECT * FROM `schedules` WHERE `id` = ? LIMIT 1", id).StructScan(schedule); err != nil {

		sendErrorJSON(w, err, 500)
		return
	}

	if err := getReservations(r, schedule); err != nil {
		sendErrorJSON(w, err, 500)
		return
	}

	sendJSON(w, schedule, 200)
}

func htmlHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	realPath := filepath.Join(publicDir, path)

	if stat, err := os.Stat(realPath); !os.IsNotExist(err) && !stat.IsDir() {
		fs.ServeHTTP(w, r)
		return
	} else {
		realPath = filepath.Join(publicDir, "index.html")
	}

	file, err := os.Open(realPath)
	if err != nil {
		sendErrorJSON(w, err, 500)
		return
	}
	defer file.Close()

	w.Header().Add("Cache-Control", fmt.Sprintf("max-age=86400, public"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	io.Copy(w, file)
}
