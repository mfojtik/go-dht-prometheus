package main

import (
	"context"
	"errors"
	//"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/d2r2/go-dht"
	"github.com/d2r2/go-logger"
	"github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	lastTemperatureGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dht_last_temperature",
		Help: "Last measured temperature by DHT sensor",
	})
	lastHumidityGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dht_last_humidity",
		Help: "Last measured humidity by DHT sensor",
	})
	last_successful_measurement_seconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dht_last_successful_measurement_seconds",
		Help: "Number of seconds that passed from the last successfully measurement",
	})
	last_measurement_retries = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dht_last_measurement_retries",
		Help: "Number of retries by DHT sensor since it got values",
	})
)

var opts struct {
	Verbose []bool `short:"v" long:"verbose" description:"Show verbose debug information"`

	SensorType       uint          `long:"sensor-type" description:"DHT sensor type" default:"3"`
	SensorPIN        uint          `long:"sensor-pin" description:"DHT sensor PIN" default:"4"`
	SensorMaxRetries uint          `long:"sensor-max-retries" description:"maximum sensor retries" default:"5"`
	ListenAddr       string        `short:"l" long:"listen-addr" description:"listen address:port" required:"true" default:":2112"`
	ReadSeconds      time.Duration `long:"interval" description:"interval between measurements" default:"5s"`
}

var log = logger.NewPackageLogger("dht",
	//logger.DebugLevel,
	logger.InfoLevel,
)

func recordMetrics() {
	last_measurement_time := time.Now()
	for {
		temperature, humidity, retried, err := dht.ReadDHTxxWithRetry(
			dht.SensorType(opts.SensorType),
			int(opts.SensorPIN),
			false,
			int(opts.SensorMaxRetries),
		)
		if err != nil {
			log.Infof("ERROR: DHT sensor reported: %v", err)
		}

		log.Infof("DHT: %.2f C, %.2f%%", temperature, humidity)

		// record amount of seconds since the last successful measurement
		last_successful_measurement_seconds.Set(float64(time.Now().Unix() - last_measurement_time.Unix()))
		last_measurement_time = time.Now()
		lastTemperatureGauge.Set(float64(temperature))
		lastHumidityGauge.Set(float64(humidity))
		last_measurement_retries.Set(float64(retried))

		time.Sleep(opts.ReadSeconds)
	}
}

func main() {
	if _, err := flags.Parse(&opts); err != nil {
		os.Exit(1)
	}

	server := &http.Server{
		Addr: opts.ListenAddr,
	}

	go recordMetrics()
	http.Handle("/metrics", promhttp.Handler())

	go func() {
		log.Infof("Starting HTTP server on %s ...", opts.ListenAddr)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
		log.Infof("Stopped serving new connections.")
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("HTTP shutdown error: %v", err)
	}
}
