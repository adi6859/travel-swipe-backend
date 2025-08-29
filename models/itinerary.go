package models

type Itinerary struct {
    Destination string   `json:"destination"`
    Days        []string `json:"days"`
}
