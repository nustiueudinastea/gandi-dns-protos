package main

import (
	"fmt"
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

type record struct {
	dns    gandi.ZoneRecord
	action string
}

//
// Gandi methods
//

func pdnsToString(dnsrsc *resource.DNSResource) string {
	return fmt.Sprintf("{\"%s\" %s %d \"%s\"}", dnsrsc.Host, dnsrsc.Type, dnsrsc.TTL, dnsrsc.Value)
}

func gdnsToString(record gandi.ZoneRecord) string {
	return fmt.Sprintf("{\"%s\" %s %d \"%s\"}", record.RrsetName, record.RrsetType, record.RrsetTTL, record.RrsetValues)
}

func convertToZoneRecord(rscID string, record resource.DNSResource) gandi.ZoneRecord {
	// ToDo: improve this. For now adding a default priority of 10
	if record.Type == "MX" {
		record.Value = "10 " + record.Value
	}

	if record.Host == "@" {
		record.Host = domain
	}

	return gandi.ZoneRecord{RrsetHref: rscID, RrsetType: record.Type, RrsetName: record.Host, RrsetTTL: record.TTL, RrsetValues: []string{record.Value}}
}

func compareRecords(precord gandi.ZoneRecord, grecord gandi.ZoneRecord) bool {
	if strings.TrimSuffix(precord.RrsetValues[0], ".") == strings.TrimSuffix(grecord.RrsetValues[0], ".") && strings.ToLower(precord.RrsetName) == strings.ToLower(grecord.RrsetName) && precord.RrsetTTL-120 < grecord.RrsetTTL && precord.RrsetTTL+120 > grecord.RrsetTTL && strings.ToLower(precord.RrsetType) == strings.ToLower(grecord.RrsetType) {
		return true
	}
	return false
}

func compareAllRecords(precords []gandi.ZoneRecord, grecords []gandi.ZoneRecord) map[string]record {
	recordsToChange := map[string]record{}

	for _, precord := range precords {
		found := false
		for _, grecord := range grecords {
			if precord.RrsetName == grecord.RrsetName {
				found = true
				if compareRecords(precord, grecord) == false {
					recordsToChange[precord.RrsetName] = record{dns: precord, action: "update"}
				}
				break
			}
		}

		if found == false {
			recordsToChange[precord.RrsetName] = record{dns: precord, action: "create"}
		}
	}

	for _, grecord := range grecords {
		found := false
		for _, precord := range precords {
			if grecord.RrsetName == precord.RrsetName {
				found = true
				break
			}
		}

		if found == false {
			recordsToChange[grecord.RrsetName] = record{dns: grecord, action: "delete"}
		}
	}

	return recordsToChange
}

func setRecord(domain string, record gandi.ZoneRecord, update bool) error {
	var err error
	if update == false {
		_, err = gclient.CreateDomainRecord(domain, record.RrsetName, record.RrsetType, record.RrsetTTL, record.RrsetValues)
		if err != nil {
			return errors.Wrapf(err, "Failed to update resource %s(%s) in Gandi", record.RrsetHref, gdnsToString(record))
		}
		log.Debugf("Resource %s(%s) has been created", record.RrsetHref, gdnsToString(record))
	} else {
		_, err = gclient.ChangeDomainRecords(domain, []gandi.ZoneRecord{record})
		if err != nil {
			return errors.Wrapf(err, "Failed to update resource %s(%s) in Gandi", record.RrsetHref, gdnsToString(record))
		}
		log.Debugf("Record %s(%s) has been updated", record.RrsetHref, gdnsToString(record))
	}

	err = pclient.SetResourceStatus(record.RrsetHref, "created")
	if err != nil {
		return errors.Wrapf(err, "Failed to set status for resource %s", record.RrsetHref)
	}
	return nil
}

func deleteRecord(domain string, record gandi.ZoneRecord) error {
	err := gclient.DeleteDomainRecord(domain, record.RrsetName, record.RrsetType)
	if err != nil {
		return errors.Wrapf(err, "Failed to delete resource %s(%s) in Gandi", record.RrsetHref, gdnsToString(record))
	}
	log.Debugf("Record %s(%s) has been deleted", record.RrsetHref, gdnsToString(record))
	return nil
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
	dnsrsc, ok := args[0].(*resource.Resource)
	if ok != true {
		log.Error("Payload is not a Protos resource")
		return
	}

	precord, ok := dnsrsc.Value.(*resource.DNSResource)
	if ok != true {
		log.Errorf("Resource %s is not of type DNS", dnsrsc.ID)
		return
	}

	protosRecord := convertToZoneRecord(dnsrsc.ID, *precord)

	log.Debugf("Checking dns resource %s(%s)", dnsrsc.ID, gdnsToString(protosRecord))
	grecord, err := gclient.GetDomainRecordWithNameAndType(domain, protosRecord.RrsetName, protosRecord.RrsetType)
	if err != nil {
		if strings.Contains(err.Error(), "Can't find the DNS record") {
			// Could not find record. Creating it.s
			log.Infof("Could not find DNS resource %s (%s) in Gandi. Creating it", dnsrsc.ID, gdnsToString(protosRecord))
			err = setRecord(domain, protosRecord, false)
			if err != nil {
				log.Error(err)
				return
			}
			return
		}
		// Different error, just print it
		log.Error("Failed to retrieve record from Gandi: ", err)
		return

	}

	// Recound found. Compare and modify it if different
	if compareRecords(protosRecord, grecord) {
		return
	}

	log.Infof("DNS resource %s (%s) is not in sync with Gandi. Updating it", dnsrsc.ID, gdnsToString(protosRecord))
	err = setRecord(domain, protosRecord, true)
	if err != nil {
		log.Error(err)
		return
	}
	log.Debugf("DNS resource %s (%s) has been updated", dnsrsc.ID, gdnsToString(protosRecord))

}

// periodicCheckAllResources is a handler that gets called periodically by the event loop.
// Used to check all resources periodically in case any updates were missed
func checkAllResources(args ...interface{}) {
	log.Debug("Checking all DNS resources in Protos")

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
		protosRecord := convertToZoneRecord(rsc.ID, *record)
		protosRecords = append(protosRecords, protosRecord)
	}

	// Retrieving all Gandi DNS records
	gandiRecords, err := gclient.ListDomainRecords(domain)
	if err != nil {
		log.Error("Could not retrieve DNS records from Gandi: ", err.Error())
	}

	recordsToChange := compareAllRecords(protosRecords, gandiRecords)

	for _, record := range recordsToChange {
		var err error
		if record.action == "create" {
			err = setRecord(domain, record.dns, false)
		} else if record.action == "update" {
			err = setRecord(domain, record.dns, true)
		} else if record.action == "delete" {
			err = deleteRecord(domain, record.dns)
		} else {
			log.Fatalf("Action %s not recognized for resource %s", record.action, record.dns.RrsetHref)
		}
		if err != nil {
			log.Error(err)
		}
	}

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

	log.SetLevel(logrus.DebugLevel)

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
