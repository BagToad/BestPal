# `/game-thread` Command

## Overview

The `/game-thread` command is a standalone command that allows users to quickly find game threads in the LFG forum. It features Discord's autocomplete functionality to show matching threads as users type.

## Command Syntax

```
/game-thread <search-query>
```

### Parameters

- `search-query` (required, autocomplete enabled): The name of the game to search for

## Functionality

### 1. **Autocomplete**
When a user starts typing in the `search-query` field, the command provides real-time autocomplete suggestions based on the LFG thread cache:

- **Empty input**: Shows up to 25 threads from the cache
- **With input**: Shows up to 25 matching threads, ranked by relevance:
  - Exact matches (highest priority)
  - Starts with the input (second priority)
  - Word boundary matches (third priority)
  - Contains the input (fourth priority)

### 2. **Search Behavior**
The command searches the in-memory LFG thread cache using the `LFGCacheSearch` function, which:
- Performs exact match lookups (case-insensitive)
- Falls back to partial matches if no exact match is found
- Returns the most relevant thread

### 3. **Response Types**

#### Success Response
When a thread is found:
- Shows an embed with "🎮 Game Thread Found" title
- Displays the thread name
- Provides a clickable link to the thread
- Response is ephemeral (only visible to the user)

#### Not Found Response
When no thread is found:
- Shows a "🔍 No Results" embed
- Displays a message: "No game thread found for **\"{search-query}\"**"
- Response is ephemeral (only visible to the user)

### 4. **Cache Validation**
The command validates that found threads still exist:
- Fetches the channel object from Discord
- Verifies it belongs to the correct forum
- Cleans up stale cache entries if a thread no longer exists

## Implementation Details

### Files Modified

1. **`internal/commands/modules/lfg/lfg_module.go`**
   - Added new `game-thread` command registration
   - Added `HandleAutocomplete` method to route autocomplete requests

2. **`internal/commands/modules/lfg/lfg.go`**
   - Added `handleGameThread` function to process the command
   - Added `handleGameThreadAutocomplete` function to handle autocomplete requests

3. **`internal/commands/module_handler.go`**
   - Added `HandleAutocomplete` method to route autocomplete interactions

4. **`internal/bot/bot.go`**
   - Added autocomplete interaction handling to `onInteractionCreate`

### Key Features

- **Thread-safe cache access**: Uses read locks when searching the cache
- **Smart ranking**: Prioritizes exact matches and prefix matches over contains matches
- **Graceful degradation**: Handles missing/invalid threads by cleaning up cache
- **User-friendly**: All responses are ephemeral (private to the user)
- **Performance optimized**: Limits autocomplete results to 25 items (Discord's max)

## Usage Example

1. User types `/game-thread`
2. User starts typing "leag" in the search-query field
3. Autocomplete shows:
   - "League of Legends"
   - "League of Legends: Wild Rift"
   - etc.
4. User selects "League of Legends" from autocomplete or continues typing
5. User submits the command
6. Bot responds with an embed showing the thread link

## Error Handling

- **Forum not configured**: Shows error message if `LFG_FORUM_CHANNEL_ID` is not set
- **Empty query**: Shows error message if search query is empty
- **Thread not found**: Shows "No Results" embed with appropriate message
- **Stale cache entry**: Removes invalid entries and shows "No Results" message

## Future Enhancements

Potential improvements could include:
- Fuzzy matching for typos
- Recently accessed threads prioritization
- Category-based filtering (e.g., by genre)
- Thread metadata in autocomplete (e.g., member count, activity level)
