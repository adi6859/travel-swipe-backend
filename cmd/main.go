package main

import (
    "github.com/gin-gonic/gin"
    "travel-swipe-backend/controllers"
)

func main() {
    r := gin.Default()

    r.GET("/swipe/destinations", controllers.GetDestinations)
    r.GET("/itinerary", controllers.GenerateItinerary)
    r.POST("/booking", controllers.BookTrip)

    r.Run(":8080")
}
