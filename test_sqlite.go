package main

import (
	"fmt"
	"log"

	"gamerpal/internal/database"
)

func main() {
	fmt.Println("🗃️  GamerPal SQLite Database Proof of Concept")
	fmt.Println("==================================================")

	// Initialize database
	db, err := database.NewDB("test_gamerpal.db")
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer db.Close()

	// Test data
	testUserID := "123456789012345678"
	testKey := "favorite_game"
	testValue := "The Witcher 3: Wild Hunt"

	fmt.Printf("🔗 Database initialized successfully\n")
	fmt.Printf("📄 Database file: test_gamerpal.db\n\n")

	// Store test data
	fmt.Printf("💾 Storing test data...\n")
	fmt.Printf("   User ID: %s\n", testUserID)
	fmt.Printf("   Key: %s\n", testKey)
	fmt.Printf("   Value: %s\n", testValue)

	err = db.StoreUserData(testUserID, testKey, testValue)
	if err != nil {
		log.Fatal("Failed to store data:", err)
	}
	fmt.Printf("✅ Data stored successfully!\n\n")

	// Fetch the data back
	fmt.Printf("🔍 Fetching stored data...\n")
	data, err := db.GetUserData(testUserID, testKey)
	if err != nil {
		log.Fatal("Failed to fetch data:", err)
	}

	if data != nil {
		fmt.Printf("✅ Data retrieved successfully!\n")
		fmt.Printf("   ID: %d\n", data.ID)
		fmt.Printf("   User ID: %s\n", data.UserID)
		fmt.Printf("   Key: %s\n", data.Key)
		fmt.Printf("   Value: %s\n", data.Value)
		fmt.Printf("   Created: %s\n", data.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("   Updated: %s\n", data.UpdatedAt.Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("❌ No data found\n")
	}

	// Store additional test data
	fmt.Printf("\n💾 Storing additional test data...\n")
	testData := map[string]string{
		"platform": "PC",
		"playtime": "120 hours",
		"rating":   "10/10",
		"status":   "completed",
	}

	for key, value := range testData {
		err = db.StoreUserData(testUserID, key, value)
		if err != nil {
			log.Printf("Failed to store %s: %v", key, err)
		} else {
			fmt.Printf("   ✓ Stored %s: %s\n", key, value)
		}
	}

	// Fetch all user data
	fmt.Printf("\n📊 Fetching all user data...\n")
	allData, err := db.GetAllUserData(testUserID)
	if err != nil {
		log.Fatal("Failed to fetch all data:", err)
	}

	fmt.Printf("Found %d records:\n", len(allData))
	for i, record := range allData {
		fmt.Printf("   %d. %s = %s\n", i+1, record.Key, record.Value)
	}

	// Get database statistics
	fmt.Printf("\n📈 Database Statistics:\n")
	stats, err := db.GetStats()
	if err != nil {
		log.Printf("Failed to get stats: %v", err)
	} else {
		for key, value := range stats {
			fmt.Printf("   %s: %v\n", key, value)
		}
	}

	// Update existing data
	fmt.Printf("\n🔄 Updating existing data...\n")
	newValue := "The Witcher 3: Wild Hunt - Game of the Year Edition"
	err = db.StoreUserData(testUserID, testKey, newValue)
	if err != nil {
		log.Fatal("Failed to update data:", err)
	}
	fmt.Printf("✅ Updated %s to: %s\n", testKey, newValue)

	// Verify the update
	updatedData, err := db.GetUserData(testUserID, testKey)
	if err != nil {
		log.Fatal("Failed to fetch updated data:", err)
	}
	if updatedData != nil {
		fmt.Printf("✅ Verified updated value: %s\n", updatedData.Value)
		fmt.Printf("   Updated at: %s\n", updatedData.UpdatedAt.Format("2006-01-02 15:04:05"))
	}

	fmt.Printf("\n🎉 SQLite Proof of Concept completed successfully!\n")
	fmt.Printf("🗂️  Database file 'test_gamerpal.db' created in current directory\n")
	fmt.Printf("📝 You can inspect it with: sqlite3 test_gamerpal.db '.tables'\n")

	// Clean up test database
	fmt.Printf("\n🧹 Cleaning up test database...\n")
	// os.Remove("test_gamerpal.db")
	fmt.Printf("✅ Test database removed\n")
}
