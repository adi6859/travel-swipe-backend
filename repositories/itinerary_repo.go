package repositories

import "travel-swipe-backend/models"

func GenerateMockItinerary(destination string) models.Itinerary {
    return models.Itinerary{
        Destination: destination,
        Days: []string{
            "Day 1: Arrival & City Tour",
            "Day 2: Explore Local Attractions",
            "Day 3: Adventure / Beach Day",
            "Day 4: Cultural Experience",
            "Day 5: Shopping & Departure",
        },
    }
}
