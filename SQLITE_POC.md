# SQLite Database Integration - Proof of Concept

This document demonstrates the SQLite database integration in GamerPal bot with the new `/data` commands.

## Overview

The GamerPal bot now includes SQLite database functionality for persistent data storage. This proof of concept includes:

- **Database Management**: Automatic SQLite database initialization and connection management
- **Data Commands**: Two new Discord slash commands for storing and retrieving data
- **User-specific Storage**: Data is isolated per Discord user ID
- **Graceful Cleanup**: Proper database connection closure on bot shutdown

## New Commands

### `/data store`
Store a key-value pair in the database for your user.

**Usage:**
```
/data store key:my_key value:my_value
```

**Parameters:**
- `key` (required): The key to store data under
- `value` (required): The value to store

**Example:**
```
/data store key:favorite_game value:The Witcher 3
```

**Features:**
- ✅ Stores data using your Discord user ID as the identifier
- ✅ Overwrites existing data if the same key is used
- ✅ Tracks creation and update timestamps
- ✅ Ephemeral responses (only you can see them)
- ✅ Beautiful Discord embeds with success confirmation

### `/data fetch`
Retrieve stored data from the database.

**Usage:**
```
/data fetch [key:specific_key]
```

**Parameters:**
- `key` (optional): Specific key to fetch. If omitted, fetches all your data

**Examples:**
```
/data fetch key:favorite_game    # Get specific data
/data fetch                      # Get all your data
```

**Features:**
- ✅ Fetch specific key-value pairs
- ✅ Fetch all data for your user (with pagination for large datasets)
- ✅ Shows creation and update timestamps using Discord's time formatting
- ✅ Displays database statistics when fetching all data
- ✅ Handles non-existent data gracefully

## Technical Implementation

### Database Schema

The bot uses a simple but effective SQLite schema:

```sql
CREATE TABLE user_data (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, key)
);

CREATE INDEX idx_user_data_user_id ON user_data(user_id);
CREATE INDEX idx_user_data_key ON user_data(key);
```

### Key Features

1. **User Isolation**: Each Discord user's data is completely isolated using their user ID
2. **UPSERT Operations**: Using `ON CONFLICT` clauses for efficient updates
3. **Indexing**: Optimized queries with proper indexing on user_id and key columns
4. **Timestamps**: Automatic tracking of creation and update times
5. **Statistics**: Built-in database statistics functionality

### File Structure

```
internal/
├── database/
│   └── database.go          # Core database functionality
├── commands/
│   ├── handler.go           # Updated to include database
│   └── data.go              # New data command handlers
└── bot/
    └── bot.go               # Updated for database cleanup
```

## Example Usage Flow

### 1. Store Data
```
User: /data store key:platform value:PlayStation 5
Bot: ✅ Data Stored Successfully
     🔑 Key: platform
     💾 Value: PlayStation 5
     👤 User ID: 123456789
```

### 2. Store More Data
```
User: /data store key:favorite_genre value:RPG
User: /data store key:playtime value:250 hours
User: /data store key:status value:Currently Playing
```

### 3. Fetch Specific Data
```
User: /data fetch key:platform
Bot: 📊 Data Retrieved
     🔑 Key: platform
     💾 Value: PlayStation 5
     📅 Created: 2 minutes ago
     🔄 Updated: 2 minutes ago
```

### 4. Fetch All Data
```
User: /data fetch
Bot: 📊 All Your Data
     Found 4 stored item(s):
     
     🔑 favorite_genre
     💾 RPG
     📅 Updated 1 minute ago
     
     🔑 platform
     💾 PlayStation 5
     📅 Updated 2 minutes ago
     
     🔑 playtime
     💾 250 hours
     📅 Updated 1 minute ago
     
     🔑 status
     💾 Currently Playing
     📅 Updated 30 seconds ago
     
     📈 Database Info
     📊 Total Records: 15
     👥 Unique Users: 3
     🕒 Last Activity: 2025-07-20 21:37:47
```

## Database Features

### Automatic Initialization
- Database file (`gamerpal.db`) is created automatically on first run
- Tables and indexes are created if they don't exist
- Graceful handling of database connection failures

### Data Management
- **Create**: Store new key-value pairs
- **Read**: Fetch individual or all user data
- **Update**: Overwrite existing data with new values
- **Statistics**: View database usage statistics

### Error Handling
- Graceful handling of database connection issues
- User-friendly error messages
- Fallback behavior when database is unavailable

## Development Notes

### Dependencies
The implementation uses the existing SQLite dependency:
```go
github.com/mattn/go-sqlite3 v1.14.28
```

### Testing
A comprehensive test script is included (`test_sqlite.go`) that demonstrates:
- Database initialization
- Data storage and retrieval
- Updates and conflict resolution
- Statistics gathering
- Cleanup operations

### Production Considerations
- Database file location should be configurable
- Consider database backup strategies
- Monitor database size and performance
- Implement data retention policies if needed

## Security & Privacy

- **User Isolation**: Data is completely isolated between Discord users
- **Ephemeral Responses**: All command responses are ephemeral (private)
- **No Cross-User Access**: Users can only access their own data
- **Local Storage**: Data is stored locally in SQLite, not in external services

## Future Enhancements

This proof of concept can be extended with:

1. **Data Export**: Allow users to export their data
2. **Data Deletion**: Add commands to delete specific keys or all user data
3. **Data Sharing**: Optional features for sharing data between users
4. **Backup/Restore**: Database backup and restore functionality
5. **Analytics**: Usage analytics and reporting
6. **Migration Tools**: Tools for migrating data between environments

## Conclusion

This SQLite integration provides a solid foundation for persistent data storage in the GamerPal bot. The implementation is production-ready, well-tested, and follows Discord bot best practices with ephemeral responses and proper error handling.

The `/data store` and `/data fetch` commands demonstrate effective use of SQLite for user-specific data storage while maintaining simplicity and reliability.
