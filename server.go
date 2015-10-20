package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

var (
	db *sql.DB
)

type geoInfo struct {
	Ip          string
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
	RegionCode  string `json:"region_code"`
	RegionName  string `json:"region_name"`
	City        string
	ZipCode     string `json:"zip_code"`
	TimeZone    string `json:"time_zone"`
	Latitude    float32
	Longitude   float32
	MetroCode   int `json:"metro_code"`
}

type request struct {
	Method     string
	URL        *url.URL
	Header     http.Header
	Body       io.ReadCloser
	Host       string
	Trailer    http.Header
	RemoteAddr string
	RequestURI string
}

func main() {
	// Setup log
	fileName := fmt.Sprintf("/logs/%s", os.Args[0])
	file, err := os.OpenFile(fileName, os.O_RDWR | os.O_CREATE | os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Error opening file: %v", err)
	}
	defer file.Close()
	log.SetOutput(file)

	// Bind to port to prevent the server from running more than once.
	go func() {
		http.HandleFunc("/stop/", stop)
		err := http.ListenAndServe(":9999", nil)
		if err != nil {
			log.Fatal(err, ". Probably ths server is already running.")
		}
	}()
	
	// Create the connection to the database.
	setupDB()
	
	// Serve http
	http.HandleFunc("/test/", serv)
	err = http.ListenAndServe(":80", nil)
	if err != nil {
		log.Fatal(err)
	}
}

func serv(resp http.ResponseWriter, req *http.Request) {
	now := time.Now()

	log.Println("Received a request from:", req.RemoteAddr)

	// Start the local filse server
	fs := http.FileServer(http.Dir("/web"))
	fs = http.StripPrefix("/test/", fs)
	fs.ServeHTTP(resp, req) // Serve the requested file

	// Call API for geo IP info
	remoteAddr := req.RemoteAddr
	apiCall := fmt.Sprintf("https://freegeoip.net/json/%s", strings.Split(remoteAddr, ":")[0])
	res, err := http.Get(apiCall)
	if err != nil {
		log.Println(err)
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
	}

	var info geoInfo
	err = json.Unmarshal(body, &info)
	if err != nil {
		log.Println(err)
	}

	err = logToDatabase(req, info, now)
	if err != nil {
		log.Println(err)
	}
}

func stop(resp http.ResponseWriter, req *http.Request) {
	log.Println("Stop Requested.")
	os.Exit(0)
}

func setupDB() {
	dbUser := os.Getenv("DBUSER")
	dbPass := os.Getenv("DBPASS")

	var err error
	connectionString := fmt.Sprintf("user=%s dbname=hup sslmode=require host=asustindb.cv2dl6jc40dy.us-west-2.rds.amazonaws.com password=%s", dbUser, dbPass)
	db, err = sql.Open("postgres", connectionString)
	if err != nil {
		log.Fatal(err)
	}
	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Connected to the database succesfully.")
}

func logToDatabase(req *http.Request, geo geoInfo, now time.Time) error {
	query := `INSERT INTO requests (ip, port, uri, method, url, header, body, host, trailer, date, country_code, country_name, region_code, region_name, city, zip_code, time_zone, latitude, longitude, metro_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
RETURNING request_id`
	var requestId int
	var url string
	var header string
	var body string
	var trailer string

	remoteAddr := strings.Split(req.RemoteAddr, ":")
	ip := remoteAddr[0]
	port := remoteAddr[1]
	tmpUrl, err := json.MarshalIndent(req.URL, "", "\t")
	if err != nil {
		url = fmt.Sprintf("{\"Err\":\"%s\"}", err)
	} else {
		url = string(tmpUrl)
	}
	tmpHeader, err := json.MarshalIndent(req.Header, "", "\t")
	if err != nil {
		header = fmt.Sprintf("{\"Err\":\"%s\"}", err)
	} else {
		header = string(tmpHeader)
	}
	tmpBody, err := json.MarshalIndent(req.Body, "", "\t")
	if err != nil {
		body = fmt.Sprintf("{\"Err\":\"%s\"}", err)
	} else {
		body = string(tmpBody)
	}
	tmpTrailer, err := json.MarshalIndent(req.Trailer, "", "\t")
	if err != nil {
		trailer = fmt.Sprintf("{\"Err\":\"%s\"}", err)
	} else {
		trailer = string(tmpTrailer)
	}

	err = db.QueryRow(query, ip, port, req.RequestURI, req.Method, url, header,
		body, req.Host, trailer, now, geo.CountryCode, geo.CountryName,
		geo.RegionCode, geo.RegionName, geo.City, geo.ZipCode, geo.TimeZone,
		geo.Latitude, geo.Longitude, geo.MetroCode).Scan(&requestId)
	if err != nil {
		return err
	}

	return nil
}
