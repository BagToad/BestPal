# GamerPal Bot Usage Examples

## Prune Inactive Users Command

The `/prune-inactive` command helps server administrators clean up inactive users who haven't been assigned any roles.

### Basic Usage

#### 1. Dry Run (Default - Safe)
```
/prune-inactive
```
- **Safe**: Does not remove any users
- **Preview**: Shows which users would be removed
- **Analysis**: Provides statistics about users without roles

#### 2. Execute Prune
```
/prune-inactive execute:true
```
- **Action**: Actually removes users without roles
- **Requires**: Administrator permissions
- **Logs**: Records removal with reason "Pruned: User had no roles"

### Example Output

#### Dry Run Example
```
🔍 Prune Inactive Users - Dry Run

This is a dry run. No users will be removed.

Found 5 users without roles:
• john_doe#1234
• inactive_user#5678 (Nick: Old Name)
• test_account#9012
• guest_user#3456
• unused_account#7890

Total Members Checked: 150
Users Without Roles: 5
```

#### Execute Example
```
🔨 Prune Inactive Users - Execution

✅ Successfully removed 5 users without roles.

Found 5 users without roles:
• john_doe#1234
• inactive_user#5678 (Nick: Old Name)
• test_account#9012
• guest_user#3456
• unused_account#7890

Total Members Checked: 150
Users Without Roles: 5
Users Removed: 5
```

### Safety Features

1. **Administrator Only**: Command requires Administrator permissions
2. **Bot Protection**: Never removes bots from the server
3. **Dry Run Default**: Safe by default - won't remove users unless explicitly told to
4. **Detailed Preview**: Shows exactly which users will be affected
5. **Audit Trail**: All removals are logged with a clear reason

### Best Practices

1. **Always test first**: Run without `execute:true` to see what will happen
2. **Check the list**: Review the users that will be removed before executing
3. **Regular maintenance**: Run periodically to keep your server clean
4. **Monitor logs**: Check your server's audit log after running

### Troubleshooting

- **"You need Administrator permissions"**: Only server administrators can use this command
- **"Error fetching server members"**: The bot may not have proper permissions
- **Some users not removed**: The bot can only remove users with roles lower than its own highest role

### Security Considerations

- Only grant Administrator permissions to trusted users
- Consider creating a dedicated role for bot management
- Regularly review who has access to prune commands
- Always verify the user list before executing
