package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

var DB *pgx.Conn

type Schedule struct {
	Medicine  string    `json:"medicine"`
	Frequency int       `json:"frequency"`
	Duration  int       `json:"duration"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
}

type TakeSchedule struct {
	Medicine string `json:"medicine"`
	TakeTime string `json:"take_time"`
}

const PPH = 12

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	DB, err = pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Printf("failed to open database: %v", err)
		return
	}

	defer DB.Close(context.Background())

	http.HandleFunc("/schedule", scheduleHandler)
	http.HandleFunc("/schedules", getAllUserSchedulesHandler)
	http.HandleFunc("/next_takings", getNextTakingsHandler)
	http.HandleFunc("/delete", deleteScheduleHandler)

	fmt.Println("starting ...")

	err = http.ListenAndServe("localhost:3333", nil)
	if err != nil {
		log.Println(err)
		return
	}
}

func scheduleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		createScheduleHandler(w, r)
	} else if r.Method == http.MethodGet {
		getOneUserScheduleHandler(w, r)
	} else {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func createScheduleHandler(w http.ResponseWriter, r *http.Request) {
	var schedule Schedule
	err := json.NewDecoder(r.Body).Decode(&schedule)
	if err != nil {
		http.Error(w, "invalid schedule format", http.StatusBadRequest)
		return
	}

	var scheduleID int
	query := `INSERT INTO schedule (medicine, frequency, duration, user_id) VALUES ($1, $2, $3, $4) RETURNING id`
	err = DB.QueryRow(context.Background(), query, schedule.Medicine, schedule.Frequency, schedule.Duration, schedule.UserID).Scan(&scheduleID)
	if err != nil {
		http.Error(w, "error adding data to database", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "schedule saved with ID: %d\n", scheduleID)
}

func getOneUserScheduleHandler(w http.ResponseWriter, r *http.Request) {
	requiredParams := []string{"user_id", "schedule_id"}
	urlParams := r.URL.Query()
	missingParamMessage := checkRequiredParams(requiredParams, urlParams)
	if missingParamMessage != "" {
		http.Error(w, missingParamMessage, http.StatusBadRequest)
		return
	}

	userID := urlParams.Get("user_id")
	scheduleID := urlParams.Get("schedule_id")
	var schedule Schedule
	query := "SELECT medicine, frequency, duration, user_id, created_at FROM schedule WHERE user_id = $1 AND id = $2"
	err := DB.QueryRow(context.Background(), query, userID, scheduleID).Scan(&schedule.Medicine, &schedule.Frequency, &schedule.Duration, &schedule.UserID, &schedule.CreatedAt)
	if err != nil {
		http.Error(w, "failed get schedule from database", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, convertToJson(schedule))
}

func getAllUserSchedulesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requiredParams := []string{"user_id"}
	urlParams := r.URL.Query()
	missingParamMessage := checkRequiredParams(requiredParams, urlParams)
	if missingParamMessage != "" {
		http.Error(w, missingParamMessage, http.StatusBadRequest)
		return
	}

	userID := urlParams.Get("user_id")
	query := "SELECT medicine, frequency, duration, user_id, created_at FROM schedule WHERE user_id = $1"
	rows, err := DB.Query(context.Background(), query, userID)
	if err != nil {
		http.Error(w, "failed get schedules from database", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var schedules []Schedule
	for rows.Next() {
		var schedule Schedule
		err := rows.Scan(&schedule.Medicine, &schedule.Frequency, &schedule.Duration, &schedule.UserID, &schedule.CreatedAt)
		if err != nil {
			fmt.Fprintf(w, "failed get schedule")
			return
		}
		schedules = append(schedules, schedule)
	}

	if len(schedules) == 0 {
		fmt.Fprintf(w, "no schedules for this user")
		return
	}

	for _, schedule := range schedules {
		fmt.Fprintf(w, convertToJson(schedule))
	}
}

func getNextTakingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requiredParams := []string{"user_id"}
	urlParams := r.URL.Query()
	missingParamMessage := checkRequiredParams(requiredParams, urlParams)
	if missingParamMessage != "" {
		http.Error(w, missingParamMessage, http.StatusBadRequest)
		return
	}

	userID := urlParams.Get("user_id")
	query := "SELECT medicine, frequency, duration, user_id, created_at FROM schedule WHERE user_id = $1"
	rows, err := DB.Query(context.Background(), query, userID)
	if err != nil {
		http.Error(w, "failed get schedules from database", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var schedules []Schedule
	for rows.Next() {
		var schedule Schedule
		err := rows.Scan(&schedule.Medicine, &schedule.Frequency, &schedule.Duration, &schedule.UserID, &schedule.CreatedAt)
		if err != nil {
			fmt.Fprintf(w, "failed get schedule")
			return
		}
		schedules = append(schedules, schedule)
	}

	if len(schedules) == 0 {
		fmt.Fprintf(w, "no schedules for this user")
		return
	}

	var takeSchedules []TakeSchedule
	for _, schedule := range schedules {
		if !checkDay(schedule) {
			continue
		}
		takeSchedules = calculateTime(schedule)
	}

	if len(takeSchedules) > 0 {
		for _, takeSchedule := range takeSchedules {
			fmt.Fprintf(w, convertToJson(takeSchedule))
		}
	} else {
		fmt.Fprintf(w, "no schedules for the next %d hour/hours", PPH)
	}
}

func calculateTime(schedule Schedule) []TakeSchedule {
	now := time.Now()
	year, month, day := now.Date()
	startTime := time.Date(year, month, day, 8, 0, 0, 0, now.Location())
	endTime := time.Date(year, month, day, 22, 0, 0, 0, now.Location())

	totalMinutes := int(endTime.Sub(startTime).Minutes())
	intervalDuration := 0
	if schedule.Duration > 1 {
		intervalDuration = totalMinutes / (schedule.Duration - 1)
	}

	doses := make([]time.Time, schedule.Duration)
	currentTime := startTime

	for i := 0; i < schedule.Duration; i++ {
		minutes := currentTime.Minute()
		if minutes%15 != 0 {
			minutes = ((minutes / 15) + 1) * 15
		}
		roundedTime := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), currentTime.Hour(), minutes, 0, 0, currentTime.Location())
		doses[i] = roundedTime
		currentTime = currentTime.Add(time.Duration(intervalDuration) * time.Minute)
	}

	timeInterval := time.Duration(PPH) * time.Hour
	later := now.Add(timeInterval)

	var takeSchedules []TakeSchedule
	for _, doseTime := range doses {
		fmt.Println(doseTime.Format("15:04"))
		if doseTime.After(now) && doseTime.Before(later) {
			var takeSchedule TakeSchedule
			takeSchedule.Medicine = schedule.Medicine
			takeSchedule.TakeTime = doseTime.Format("15:04")
			takeSchedules = append(takeSchedules, takeSchedule)
		}
	}

	return takeSchedules
}

func checkDay(schedule Schedule) bool {
	if schedule.Frequency == 0 {
		return true
	}

	addDate, err := time.Parse("2006-01-02", schedule.CreatedAt.Format("2006-01-02"))
	if err != nil {
		return false
	}

	currentDate := time.Now().Truncate(24 * time.Hour)
	if currentDate.Before(schedule.CreatedAt) {
		return false
	}

	targetDate := addDate.AddDate(0, 0, schedule.Frequency)

	return currentDate.Before(targetDate)
}

func deleteScheduleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requiredParams := []string{"schedule_id"}
	urlParams := r.URL.Query()
	missingParamMessage := checkRequiredParams(requiredParams, urlParams)
	if missingParamMessage != "" {
		http.Error(w, missingParamMessage, http.StatusBadRequest)
		return
	}

	scheduleID := urlParams.Get("schedule_id")
	query := "DELETE FROM schedule WHERE id = $1"
	_, err := DB.Query(context.Background(), query, scheduleID)
	if err != nil {
		http.Error(w, "failed delete schedule from database", http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "delete schedule from database success")
}

func convertToJson(schedule interface{}) string {
	b, err := json.Marshal(schedule)
	if err != nil {
		return "failed convert schedule to json"
	}

	return string(b)
}

func checkRequiredParams(reqParams []string, urlParams url.Values) string {
	for _, param := range reqParams {
		if _, ok := urlParams[param]; !ok {
			return "missing required parameter: " + param
		}
	}

	return ""
}
