package main

import (
    "bytes"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "net/http"
    "os"
    "os/signal"
    "regexp"
    "strconv"
    "strings"
    "syscall"
    "time"

    "github.com/gorilla/mux"
    "github.com/lflare/mdathome-golang/diskcache"
    "github.com/hashicorp/go-retryablehttp"
)

// Global variables
var clientSettings = ClientSettings{
    CacheDirectory:             "cache/",   // Default cache directory
    ClientPort:                 44300,      // Default client port
    MaxKilobitsPerSecond:       10000,      // Default 10Mbps
    MaxCacheSizeInMebibytes:    1024,       // Default 1GB
    MaxReportedSizeInMebibytes: 1024,       // Default 1GB
    GracefulShutdownInSeconds:  60,         // Default 60s graceful shutdown
    CacheScanIntervalInSeconds: 60,         // Default 60s scan period
}
var serverResponse ServerResponse
var cache *diskcache.Cache
var timeLastRequest time.Time
var running = true
var client *http.Client

// Swap the following for backend testing
// var apiBackend := "https://mangadex-test.net"
var apiBackend = "https://api.mangadex.network"

// Client setting handler
func loadClientSettings() {
    // Read client settings
    clientSettingsJson, err := ioutil.ReadFile("settings.json")
    if err != nil {
        log.Fatalf("Failed to read client configuration file: %v", err)
    }
    err = json.Unmarshal(clientSettingsJson, &clientSettings)
    if err != nil {
        log.Fatalf("Unable to unmarshal JSON file: %v", err)
    }

    // Print client configuration
    log.Printf("Client configuration loaded: %+v", clientSettings)
}

// Server ping handler
func pingServer() ServerResponse {
    // Create settings JSON
    settings := ServerSettings{
        Secret: clientSettings.ClientSecret,
        Port: clientSettings.ClientPort,
        DiskSpace: clientSettings.MaxCacheSizeInMebibytes * 1024 * 1024, // 1GB
        NetworkSpeed: clientSettings.MaxKilobitsPerSecond * 1000 / 8, // 100Mbps
        BuildVersion: 13,
        TlsCreatedAt: nil,
    }
    settingsJson, _ := json.Marshal(&settings)

    // Ping backend server
    r, err := http.Post(apiBackend + "/ping", "application/json", bytes.NewBuffer(settingsJson))
    if err != nil {
        log.Panicf("Failed to ping control server: %v", err)
    }
    defer r.Body.Close()

    // Read response fully
    response, err := ioutil.ReadAll(r.Body)
    if err != nil {
        log.Panicf("Failed to ping control server: %v", err)
    }

    // Print server settings out
    printableResponse := string(response)
    tlsIndex := strings.Index(printableResponse, "\"tls\"")
    log.Printf("Server settings received! - %s...", string(response[:tlsIndex]))

    // Decode & unmarshal server response
    err = json.Unmarshal(response, &serverResponse)
    if err != nil {
        log.Panicf("Failed to ping control server: %v", err)
    }

    // Check struct
    if serverResponse.ImageServer == "" {
        log.Fatalf("Failed to verify server response: %s", response)
    }

    // Return server response
    return serverResponse
}

// Server ping loop handler
func BackgroundLoop() {
    // Wait 15 seconds
    log.Println("Starting background jobs!")
    time.Sleep(15 * time.Second)

    for running == true {
        // Reload client configuration
        log.Println("Reloading client configuration")
        loadClientSettings()

        // Update max cache size
        cache.UpdateCacheLimit(clientSettings.MaxCacheSizeInMebibytes * 1024 * 1024)
        cache.UpdateCacheScanInterval(clientSettings.CacheScanIntervalInSeconds)

        // Ping backend server
        pingServer()

        // Wait 15 seconds
        time.Sleep(15 * time.Second)
    }
}

// Image handler
func RequestHandler(w http.ResponseWriter, r *http.Request) {
    // Start timer
    startTime := time.Now()

    // Extract tokens
    tokens := mux.Vars(r)

    // Sanitized URL
    if tokens["image_type"] != "data" && tokens["image_type"] != "data-saver" {
        w.WriteHeader(http.StatusBadRequest)
        return
    }
    if matched, _ := regexp.MatchString(`^[0-9a-f]{32}$`, tokens["chapter_hash"]); !matched {
        w.WriteHeader(http.StatusBadRequest)
        return
    }
    if matched, _ := regexp.MatchString(`[a-zA-Z0-9]{1,4}\.(jpg|jpeg|png|gif)$`, tokens["image_filename"]); !matched {
        w.WriteHeader(http.StatusBadRequest)
        return
    }
    sanitized_url := "/" + tokens["image_type"] + "/" + tokens["chapter_hash"] + "/" + tokens["image_filename"]

    // Check if referer exists, else fake one
    if r.Header.Get("Referer") == "" {
        r.Header.Set("Referer", "None")
    }

    // Properly handle MangaDex's Referer
    re := regexp.MustCompile(`https://mangadex.org/chapter/[0-9]+`)
    if matched := re.FindString(r.Header.Get("Referer")); matched != "" {
        r.Header.Set("Referer", matched)
    }

    // Log request
    log.Printf("Request for %s - %s - %s received", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"))

    // Check if browser token exists
    if r.Header.Get("If-Modified-Since") != "" {
        // Log browser cache
        log.Printf("Request for %s - %s - %s cached by browser", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"))
        w.WriteHeader(http.StatusNotModified)
        return
    }

    // Add headers
    w.Header().Set("Access-Control-Allow-Origin", "https://mangadex.org")
    w.Header().Set("Access-Control-Expose-Headers", "*")

    // Check if image already in cache
    if imageFromCache, ok := cache.Get(sanitized_url); !ok {
        // Log cache miss
        log.Printf("Request for %s - %s - %s missed cache", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"))
        w.Header().Set("X-Cache", "MISS")

        // Send request
        imageFromUpstream, err := client.Get(serverResponse.ImageServer + sanitized_url)
        if err != nil {
            log.Panicf("ERROR: %v", err)
        }
        defer imageFromUpstream.Body.Close()

        // Set timing header
        processedTime := time.Now().Sub(startTime).Milliseconds()
        w.Header().Set("X-Time-Taken", strconv.Itoa(int(processedTime)))
        log.Printf("Request for %s - %s - %s processed in %dms", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"), processedTime)

        // Copy request to response body
        var imageBuffer bytes.Buffer
        io.Copy(w, io.TeeReader(imageFromUpstream.Body, &imageBuffer))

        // Save hash
        cache.Set(sanitized_url, imageBuffer.Bytes())
    } else {
        // Get length
        length := len(imageFromCache)
        image := make([]byte, length)
        copy(image, imageFromCache)

        // Log cache hit
        log.Printf("Request for %s - %s - %s hit cache", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"))
        w.Header().Set("X-Cache", "HIT")

        // Set timing header
        processedTime := time.Now().Sub(startTime).Milliseconds()
        w.Header().Set("X-Time-Taken", strconv.Itoa(int(processedTime)))
        log.Printf("Request for %s - %s - %s processed in %dms", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"), processedTime)

        // Convert bytes object into reader and send to client
        imageReader := bytes.NewReader(image)
        io.Copy(w, imageReader)
    }

    // Update last request
    timeLastRequest = time.Now()

    // End time
    totalTime := time.Now().Sub(startTime).Milliseconds()
    w.Header().Set("X-Time-Taken", strconv.Itoa(int(totalTime)))
    log.Printf("Request for %s - %s - %s completed in %dms", sanitized_url, r.RemoteAddr, r.Header.Get("Referer"), totalTime)
}

func ShutdownHandler() {
    // Hook on to SIGTERM
    c := make(chan os.Signal)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)

    // Start coroutine to wait for SIGTERM
    go func() {
        <-c
        // Prepare to shutdown server
        fmt.Println("Shutting down server gracefully!")

        // Flip switch
        running = false

        // Sent stop request to backend
        request := ServerRequest{
            Secret: clientSettings.ClientSecret,
        }
        requestJson, _ := json.Marshal(&request)
        r, err := http.Post(apiBackend + "/stop", "application/json", bytes.NewBuffer(requestJson))
        if err != nil {
            log.Fatalf("Failed to shutdown server gracefully: %v", err)
        }
        defer r.Body.Close()

        // Wait till last request is normalised
        timeShutdown := time.Now()
        secondsSinceLastRequest := time.Now().Sub(timeLastRequest).Seconds()
        for secondsSinceLastRequest < 15 {
            log.Printf("%.2f seconds have elapsed since CTRL-C", secondsSinceLastRequest)

            // Give up after one minute
            if time.Now().Sub(timeShutdown).Seconds() > float64(clientSettings.GracefulShutdownInSeconds) {
                log.Printf("Giving up, quitting now!")
                break
            }

            // Count time :)
            time.Sleep(1 * time.Second)
            secondsSinceLastRequest = time.Now().Sub(timeLastRequest).Seconds()
        }

        // Exit properly
        os.Exit(0)
    }()
}

func main() {
    // Prepare logging
    f, err := os.OpenFile("log/latest.log", os.O_RDWR | os.O_CREATE | os.O_APPEND, 0666)
    if err != nil {
        log.Fatalf("Failed to open log/latest.log: %v", err)
    }
    defer f.Close()
    logWriter := io.MultiWriter(os.Stdout, f)
    log.SetFlags(0)
    log.SetOutput(prefixWriter{
        f: func() string { return time.Now().Format(time.RFC3339) + " " },
        w: logWriter,
    })

    // Load client settings
    loadClientSettings()

    // Create cache
    cache = diskcache.New(clientSettings.CacheDirectory,
                          clientSettings.MaxCacheSizeInMebibytes * 1024 * 1024,
                          clientSettings.CacheScanIntervalInSeconds)
    defer cache.Close()

    // Prepare handlers
    r := mux.NewRouter()
    r.HandleFunc("/{image_type}/{chapter_hash}/{image_filename}", RequestHandler)
    r.HandleFunc("/{request_token}/{image_type}/{chapter_hash}/{image_filename}", RequestHandler)

    // Prepare server
    http.Handle("/", r)

    // Prepare client from retryablehttp
    retryClient := retryablehttp.NewClient()
    retryClient.RetryMax = 4
    client = retryClient.StandardClient()
    client.Timeout = time.Second * 15

    // Register shutdown handler
    ShutdownHandler()

    // Prepare certificates
    serverResponse := pingServer()
    keyPair, err := tls.X509KeyPair([]byte(serverResponse.Tls.Certificate), []byte(serverResponse.Tls.PrivateKey))
    if err != nil {
        log.Fatalf("Cannot parse TLS data %v - %v", serverResponse, err)
    }

    // Start ping loop
    go BackgroundLoop()

    // Start proxy server
    err = ListenAndServeTLSKeyPair(":" + strconv.Itoa(clientSettings.ClientPort), keyPair, r)
    if err != nil {
        log.Fatalf("Cannot start server: %v", err)
    }
}