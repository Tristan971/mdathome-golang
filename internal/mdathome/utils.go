package mdathome

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tcnksm/go-latest"
)

func saveClientSettings() {
	clientSettingsSampleBytes, err := json.MarshalIndent(clientSettings, "", "    ")
	if err != nil {
		log.Fatalln("Failed to marshal sample settings.json")
	}

	err = ioutil.WriteFile("settings.json", clientSettingsSampleBytes, 0600)
	if err != nil {
		log.Fatalf("Failed to create sample settings.json: %v", err)
	}
}

func loadClientSettings() {
	// Read JSON from file
	clientSettingsJSON, err := ioutil.ReadFile("settings.json")
	if err != nil {
		log.Printf("Failed to read client configuration file - %v", err)
		saveClientSettings()
		log.Fatalf("Created sample settings.json! Please edit it before running again!")
	}

	// Unmarshal JSON to clientSettings struct
	err = json.Unmarshal(clientSettingsJSON, &clientSettings)
	if err != nil {
		log.Fatalf("Unable to unmarshal JSON file: %v", err)
	}

	// Check client configuration
	if clientSettings.ClientSecret == "" {
		log.Fatalf("Empty secret! Cannot run!")
	}

	if clientSettings.CacheDirectory == "" {
		log.Fatalf("Empty cache directory! Cannot run!")
	}

	// Print client configuration
	log.Printf("Client configuration loaded: %+v", clientSettings)
}

func checkClientVersion() {
	// Prepare version check
	githubTag := &latest.GithubTag{
		Owner:             "lflare",
		Repository:        "mdathome-golang",
		FixVersionStrFunc: latest.DeleteFrontV(),
	}

	// Check if client is latest
	res, err := latest.Check(githubTag, clientVersion)
	if err != nil {
		log.Printf("Failed to check client version %s? Proceed with caution!", clientVersion)
	} else {
		if res.Outdated {
			log.Printf("Client %s is not the latest! You should update to the latest version %s now!", clientVersion, res.Current)
			log.Printf("Client starting in 10 seconds...")
			time.Sleep(10 * time.Second)
		} else {
			log.Printf("Client %s is latest! Starting client!", clientVersion)
		}
	}
}

func backgroundWorker() {
	// Wait 15 seconds
	log.Println("Starting background jobs!")
	time.Sleep(15 * time.Second)

	for running {
		// Reload client configuration
		log.Println("Reloading client configuration")
		loadClientSettings()

		// Update max cache size
		cache.UpdateCacheLimit(clientSettings.MaxCacheSizeInMebibytes * 1024 * 1024)
		cache.UpdateCacheScanInterval(clientSettings.CacheScanIntervalInSeconds)
		cache.UpdateCacheRefreshAge(clientSettings.CacheRefreshAgeInSeconds)

		// Update server response in a goroutine
		newServerResponse := backendPing()
		if newServerResponse != nil {
			serverResponse = *newServerResponse
		}

		// Wait 15 seconds
		time.Sleep(15 * time.Second)
	}
}

func serverShutdownHandler() {
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

		// Send shutdown command to backend
		backendShutdown()

		// Wait till last request is normalised
		timeShutdown := time.Now()
		secondsSinceLastRequest := time.Since(timeLastRequest).Seconds()
		for secondsSinceLastRequest < 30 {
			log.Printf("%.2f seconds have elapsed since CTRL-C", secondsSinceLastRequest)

			// Give up after one minute
			if time.Since(timeShutdown).Seconds() > float64(clientSettings.GracefulShutdownInSeconds) {
				log.Printf("Giving up, quitting now!")
				break
			}

			// Count time :)
			time.Sleep(1 * time.Second)
			secondsSinceLastRequest = time.Since(timeLastRequest).Seconds()
		}

		// Exit properly
		os.Exit(0)
	}()
}
