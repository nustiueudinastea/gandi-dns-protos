package main

import (
	"strings"
	"time"

	"github.com/pkg/errors"
	resource "github.com/protosio/protos/resource"
	protos "github.com/protosio/protoslib-go"
	"github.com/sirupsen/logrus"
	gandi "github.com/tiramiseb/go-gandi-livedns"
)

var log = logrus.New()
var protosHost = "protos:8080"
var domain string
var pclient protos.Protos
var gclient *gandi.Gandi

//
// Gandi methods
//

func compareRecords(protosRecord gandi.ZoneRecord, gandiRecord gandi.ZoneRecord) bool {
	return false
}

func compareAllRecords(protosRecords []gandi.ZoneRecord, gandiRecords []gandi.ZoneRecord) bool {
	if len(protosRecords) != len(gandiRecords) {
		return false
	}
	var matchCount = 0
	for _, precord := range protosRecords {
		for _, grecord := range gandiRecords {
			if strings.TrimSuffix(precord.RrsetValues[0], ".") == strings.TrimSuffix(grecord.RrsetValues[0], ".") && strings.ToLower(precord.RrsetName) == strings.ToLower(grecord.RrsetName) && precord.RrsetTTL-120 < grecord.RrsetTTL && precord.RrsetTTL+120 > grecord.RrsetTTL && strings.ToLower(precord.RrsetType) == strings.ToLower(grecord.RrsetType) {
				matchCount++
			}
		}
	}
	if len(protosRecords) != matchCount {
		return false
	}
	return true
}

//
// Event handlers
//

// checkDNSResource is used as an event handler called when a DNS resource is updated or created
func checkDNSResource(args ...interface{}) {
	if len(args) != 1 {
		log.Errorf("Cannot handle new message. Wrong number of arguments: %d", len(args))
		return
	}
	dnsrsc, ok := args[1].(resource.Resource)
	if ok != true {
		log.Error("Payload is not a Protos resource")
		return
	}

	dnsvalue, ok := dnsrsc.Value.(*resource.DNSResource)
	if ok != true {
		log.Error("Resource is not of type DNS")
		return
	}
	log.Info(dnsvalue)
}

// periodicCheckAllResources is a handler that gets called periodically by the event loop.
// Used to check all resources periodically in case any updates were missed
func checkAllResources(args ...interface{}) {
	log.Info("Checking all DNS resources")

	// Retrieving all Protos DNS records
	resources, err := pclient.GetResources()
	if err != nil {
		log.Error(err)
		return
	}

	protosRecords := []gandi.ZoneRecord{}
	for _, rsc := range resources {
		var record *resource.DNSResource
		record = rsc.Value.(*resource.DNSResource)
		protosRecord := gandi.ZoneRecord{RrsetName: record.Host, RrsetType: record.Type, RrsetTTL: record.TTL, RrsetValues: []string{record.Value}}
		protosRecords = append(protosRecords, protosRecord)
	}

	// Retrieving all Gandi DNS records
	gandiRecords, err := gclient.ListDomainRecords(domain)
	if err != nil {
		log.Error("Could not retrieve all DNS records from Gandi: ", err.Error())
	}

	equal := compareAllRecords(protosRecords, gandiRecords)
	if equal {
		log.Info("Records are the same. Nothing to do")
		return
	}

	log.Info("Records are NOT the same. Synchonizing")
	return
}

func terminateHandler(args ...interface{}) {
	log.Info("Received terminate call. Deregistering as a provider and shutting down.")
	err := pclient.DeregisterProvider("dns")
	if err != nil {
		log.Error(err)
	}
	return
}

//
// Util methods
//

func registerAsProvider() {
	// Each service provider needs to register with protos
	log.Info("Registering as DNS provider")
	time.Sleep(4 * time.Second) // Giving Docker some time to assign us an IP
	err := pclient.RegisterProvider("dns")
	if err != nil {
		if strings.Contains(err.Error(), "already registered") {
			log.Warn("Failed to register as DNS provider: ", strings.TrimRight(err.Error(), "\n"))
		} else {
			log.Fatal("Failed to register as DNS provider: ", err)
		}
	}
}

func addEventHandlers() {
	// Adding event handler for processing new messages. In this case it should be DNS resources updates or requests
	err := pclient.AddEventHandler(protos.EventNewMessage, checkDNSResource)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Failed to start gandi-dns provider"))
	}

	// Adding event handler for processing the timer events. In this case they are used for doing
	// a periodic check of all the provider resources in case any updates were missed
	err = pclient.AddEventHandler(protos.EventTimer, checkAllResources)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Failed to start gandi-dns provider"))
	}

	// // Adding event handler for processing new messages. In this case it should be DNS resources updates or requests
	// err = pclient.AddEventHandler(protos.EventTerminate, terminateHandler)
	// if err != nil {
	// 	log.Fatal(errors.Wrap(err, "Failed to start gandi-dns provider"))
	// }
}

func start(apikey string, timerInterval int64) {

	appID, err := protos.GetAppID()
	if err != nil {
		terminateHandler()
		log.Fatalf("Failed to start gandi-dns provider: %s", err.Error())
	}

	gclient = gandi.New(apikey, "")
	pclient = protos.NewClient(protosHost, appID)

	registerAsProvider()
	addEventHandlers()

	log.Debug("Getting domain from Protos")
	domain, err = pclient.GetDomain()
	if err != nil {
		terminateHandler()
		log.Fatalf("Failed to retrieve domain from Protos: %s", err.Error())
	}
	if domain == "" {
		terminateHandler()
		log.Fatalf("Failed to retrieve domain from Protos: empty domain")
	}
	log.Infof("Retrieved domain %s from Protos", domain)

	_, err = gclient.GetDomain(domain)
	if err != nil {
		terminateHandler()
		log.Fatalf("Failed to retrieve domain %s via the Gandi API: %s", domain, err.Error())
	}
	log.Infof("Found domain %s via the Gandi API", domain)

	// start by syncing all DNS resources
	// checkAllResources()

	// starting the WS loop with an event timer of 5 minutes
	log.Info("Opening WS connection to Protos and waiting for events")
	err = pclient.StartWSLoop(timerInterval)
	if err != nil {
		terminateHandler()
		log.Fatalf("Something went wrong in the websocket event loop: %s", err.Error())
	}
	terminateHandler()

}
