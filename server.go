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
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	_ "github.com/lib/pq"
)

const (
	ip              = "172.31.26.28"
	port            = 8080
	securePort      = 8443
	certificateFile = "/etc/letsencrypt/live/hup.asustin.net/fullchain.pem"
	keyFile         = "/etc/letsencrypt/live/hup.asustin.net/privkey.pem"
	logFile         = "/opt/logs/hup.asustin.net/standard.log"
	webRoot         = "/opt/hup/"
	dbUser          = "hup"
	dbPass          = "hup"
)

var (
	db    *sql.DB
	tmplt *template.Template
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
	file, err := os.OpenFile(logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Error opening file: %v", err)
	}
	defer file.Close()
	log.SetOutput(file)

	// Create the connection to the database.
	setupDB()
	parseTemplates()

	http.HandleFunc("/", fileServer)
	// Listen on HTTP
	go func() {
		err := http.ListenAndServe(fmt.Sprintf("%s:%d", ip, port), nil)
		if err != nil {
			if err != http.ErrServerClosed {
				log.Fatal(err)
			}
		}
	}()

	// Listen on HTTPS
	err = http.ListenAndServeTLS(fmt.Sprintf("%s:%d", ip, securePort), certificateFile, keyFile, nil)
	if err != nil {
		if err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}

}

func fileServer(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	path := r.URL.Path[len("/"):]
	if len(path) == 0 { // If accessing the root
		files, err := filepath.Glob(webRoot + "HUP*_session.mp3")
		if err != nil {
			log.Fatal(err)
		}
		sort.Strings(files)

		render(w, "index", files)
	} else {
		// Start the local file server
		fs := http.StripPrefix("/", http.FileServer(http.Dir(webRoot)))
		rw := NewResponseWriter(w)
		fs.ServeHTTP(rw, r) // Serve the requested file
	}

	// Call API for geo IP info
	remoteAddr := r.RemoteAddr
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
		return
	}

	err = logToDatabase(r, info, now)
	if err != nil {
		log.Println(err)
	}
}

func parseTemplates() {
	files, err := filepath.Glob(webRoot + "*.html")
	if err != nil {
		log.Fatal(err)
	}

	tmplt, err = template.ParseFiles(files...)
	if err != nil {
		log.Fatalln("Template parsing error: ", err)
	}

	log.Println("HTML Templates Parsed")
}

func render(w http.ResponseWriter, page string, data interface{}) {
	w.Header().Set("Vary", "Accept-Encoding")
	err := tmplt.ExecuteTemplate(w, page, data)
	if err != nil {
		log.Println(err)
	}
}

type ResponseWriter struct {
	status int
	http.ResponseWriter
}

func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{0, w}
}

func (w *ResponseWriter) Status() int {
	return w.status
}

func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func setupDB() {
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
