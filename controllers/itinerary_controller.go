package controllers

import (
    "net/http"
    "github.com/gin-gonic/gin"
    "travel-swipe-backend/services"
)

func GenerateItinerary(c *gin.Context) {
    destination := c.Query("destination")
    if destination == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "destination required"})
        return
    }
    itinerary := services.GenerateItinerary(destination)
    c.JSON(http.StatusOK, itinerary)
}
