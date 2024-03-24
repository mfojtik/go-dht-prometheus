package main

import (
	"context"
	"errors"
	"math"
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
		Namespace: "dht",
		Name:      "last_temperature",
		Help:      "Last measured temperature by DHT sensor",
	})
	lastHumidityGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "dht",
		Name:      "last_humidity",
		Help:      "Last measured humidity by DHT sensor",
	})
	lastVaporPressureDeficitGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "dht",
		Name:      "last_vapor_pressure_deficit",
		Help:      "Last vapor deficit value",
	})
	last_successful_measurement_seconds = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "dht",
		Name:      "last_successful_measurement_seconds",
		Help:      "Number of seconds that passed from the last successfully measurement",
	})
	last_measurement_retries = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "dht",
		Name:      "last_measurement_retries",
		Help:      "Number of retries by DHT sensor since it got values",
	})
)

var opts struct {
	Verbose []bool `short:"v" long:"verbose" description:"Show verbose debug information"`

	SensorType       uint          `long:"sensor-type" description:"DHT sensor type" default:"3"`
	SensorPIN        uint          `long:"sensor-pin" description:"DHT sensor PIN" default:"4"`
	SensorMaxRetries uint          `long:"sensor-max-retries" description:"maximum sensor retries" default:"5"`
	ListenAddr       string        `short:"l" long:"listen-addr" description:"listen address:port" required:"true" default:":2112"`
	ReadSeconds      time.Duration `long:"interval" description:"interval between measurements" default:"15s"`
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

		temperature64 := float64(temperature)
		humidity64 := float64(humidity)
		es := 0.6108 * math.Exp(17.27*temperature64/(temperature64+237.3))
		ea := humidity64 / 100 * es
		// this equation returns a negative value (in kPa), which while technically correct,
		// is invalid in this case because we are talking about a deficit.
		vpd := (ea - es) * -1

		log.Infof("DHT: %.2f°C, %.2f%%, VPD: %.2f", temperature, humidity, vpd)

		// record amount of seconds since the last successful measurement
		last_successful_measurement_seconds.Set(float64(time.Now().Unix() - last_measurement_time.Unix()))
		last_measurement_time = time.Now()
		lastTemperatureGauge.Set(float64(temperature))
		lastHumidityGauge.Set(float64(humidity))
		last_measurement_retries.Set(float64(retried))
		lastVaporPressureDeficitGauge.Set(vpd)

		time.Sleep(opts.ReadSeconds)
	}
}

func main() {
	defer logger.FinalizeLogger()
	if _, err := flags.Parse(&opts); err != nil {
		os.Exit(1)
	}
	if len(opts.Verbose) != 0 {
		logger.ChangePackageLogLevel("dht", logger.InfoLevel)
	}
	log.Debugf("opts: %#v", opts)

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
