package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dknopik/towersofpau"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
)

const (
	participantTime     = 20
	coordinatorTime     = 120
	immediateStartDelay = 5
	pushbackDelay       = 10
	rounds              = 10
)

func NewCoordinator(initialCeremony *towersofpau.Ceremony) *Coordinator {
	ceremonies := make([]*towersofpau.Ceremony, 0, rounds)
	ceremonies = append(ceremonies, initialCeremony)
	return &Coordinator{
		slotByTicket: make(map[string]*slot),
		slots:        make([]*slot, 0),
		ceremonies:   ceremonies,
		maxRounds:    rounds,
	}
}

type Coordinator struct {
	mutex         sync.Mutex
	slotByTicket  map[string]*slot
	slots         []*slot
	currentSlot   int
	ceremonies    []*towersofpau.Ceremony
	ceremonyMutex sync.Mutex
	maxRounds     int
}

func (c *Coordinator) RegisterParticipant(rw http.ResponseWriter, req *http.Request) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	slot := new(slot)
	slot.index = len(c.slots)
	if c.currentSlot == slot.index {
		slot.start = time.Now().Unix() + immediateStartDelay
	} else {
		slot.start = c.slots[len(c.slots)-1].deadline + coordinatorTime
	}
	slot.deadline = slot.start + participantTime
	slot.participantTicket = getTicket()
	c.slotByTicket[slot.participantTicket] = slot
	resp, err := json.Marshal(towersofpau.RegistrationResponse{
		Start:    slot.start,
		Deadline: slot.deadline,
		Ticket:   slot.participantTicket,
	})
	if err != nil {
		rw.WriteHeader(500)
		return
	}
	rw.Write(resp)
}

func getTicket() string {
	b := make([]byte, 32)
	l, err := rand.Read(b)
	if err != nil || l != 32 {
		panic("invalid randomness")
	}
	return common.Bytes2Hex(b)
}

func (c *Coordinator) RetrieveParticipant(rw http.ResponseWriter, req *http.Request) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	ticket := mux.Vars(req)["ticket"]
	slot := c.slotByTicket[ticket]
	if slot == nil || slot.index < c.currentSlot {
		rw.WriteHeader(403)
		return
	}

	response := towersofpau.FetchResponse{
		Start:    slot.start,
		Deadline: slot.deadline,
		Ceremony: nil,
	}

	// check if current slot has expired or is in processing
	for c.currentSlot < slot.index {
		if c.slots[c.currentSlot].submitted || c.slots[c.currentSlot].deadline >= time.Now().Unix() {
			break
		} else {
			c.currentSlot++
		}
	}

	if c.currentSlot == slot.index {
		if slot.deadline < time.Now().Unix() {
			c.currentSlot++
			rw.WriteHeader(403)
			return
		} else {
			ceremony := c.ceremonies[len(c.ceremonies)-1]
			jsonceremony, err := towersofpau.SerializeJSONCeremony(ceremony)
			if err != nil {
				rw.WriteHeader(500)
				return
			}
			response.Ceremony = &jsonceremony
		}
	} else if c.currentSlot < slot.index && slot.start < time.Now().Unix() {
		for _, slot := range c.slots[c.currentSlot+1:] {
			slot.start += pushbackDelay
			slot.deadline += pushbackDelay
		}
	}

	resp, err := json.Marshal(response)
	if err != nil {
		rw.WriteHeader(500)
		return
	}
	rw.Write(resp)
}

func (c *Coordinator) SubmitCeremony(rw http.ResponseWriter, req *http.Request) {
	ticket := mux.Vars(req)["ticket"]
	slot := c.slotByTicket[ticket]
	if slot == nil || slot.index < c.currentSlot {
		rw.WriteHeader(403)
		return
	}

	newCeremony, err := towersofpau.Deserialize(req.Body)
	if err != nil {
		rw.WriteHeader(400)
		return
	}

	c.ceremonyMutex.Lock()
	defer c.ceremonyMutex.Unlock()
	oldCeremony := c.ceremonies[len(c.ceremonies)-1]
	if err := towersofpau.VerifySubmission(oldCeremony, newCeremony); err != nil {
		fmt.Println(err)
		rw.WriteHeader(400)
		return
	}

	// Ceremony was valid, store it
	c.ceremonies = append(c.ceremonies, oldCeremony)
	rw.WriteHeader(200)
	return
}

type slot struct {
	index             int
	start             int64
	deadline          int64
	participantTicket string
	submitted         bool
}
