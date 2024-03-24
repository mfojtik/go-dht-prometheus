package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/d2r2/go-dht"
	"github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	lastTemperatureGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "last_temperature",
		Help: "Last measured temperature by DHT sensor",
	})
	lastHumidityGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "last_humidity",
		Help: "Last measured humidity by DHT sensor",
	})
	last_successful_measurement_seconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "last_successful_measurement_seconds",
		Help: "Number of seconds that passed from the last successfully measurement",
	})
	last_measurement_retries = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "last_measurement_retries",
		Help: "Number of retries by DHT sensor since it got values",
	})
)

var opts struct {
	Verbose []bool `short:"v" long:"verbose" description:"Show verbose debug information"`

	sensorType       uint          `long:"sensor-type" description:"DHT sensor type" default:"3"`
	sensorPIN        uint          `long:"sensor-pin" description:"DHT sensor PIN" default:"4"`
	sensorMaxRetries uint          `long:"sensor-max-retries" description:"maximum sensor retries" default:"5"`
	listenAddr       string        `short:"l" long:"listen-addr" description:"listen address:port" required:"true" default:":2112"`
	readSeconds      time.Duration `long:"interval" description:"interval between measurements" default:"5"`
}

func recordMetrics() {
	last_measurement_time := time.Now()
	for {
		temperature, humidity, retried, err := dht.ReadDHTxxWithRetry(
			dht.SensorType(opts.sensorType),
			int(opts.sensorPIN),
			false,
			int(opts.sensorMaxRetries),
		)
		if err != nil {
			log.Printf("ERROR: DHT sensor reported: %v", err)
		}

		log.Printf("DHT: %.2 C, %.2%%", temperature, humidity)

		// record amount of seconds since the last successful measurement
		last_successful_measurement_seconds.Set(float64(time.Now().Unix() - last_measurement_time.Unix()))
		last_measurement_time = time.Now()
		lastTemperatureGauge.Set(float64(temperature))
		lastHumidityGauge.Set(float64(humidity))
		last_measurement_retries.Set(float64(retried))

		time.Sleep(opts.readSeconds * time.Second)
	}
}

func main() {
	if _, err := flags.Parse(&opts); err != nil {
		log.Fatalf("ERR: %v", err)
	}

	server := &http.Server{
		Addr: opts.listenAddr,
	}

	go recordMetrics()
	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
		log.Println("Stopped serving new connections.")
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("HTTP shutdown error: %v", err)
	}
	log.Println("Graceful shutdown complete.")
}
