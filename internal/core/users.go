package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// UserEntry represents a user in the whitelist
type UserEntry struct {
	EmployeeID string `json:"employee_id"`
	Name       string `json:"name"`
	Department string `json:"department,omitempty"`
	IsAdmin    bool   `json:"is_admin,omitempty"`
}

// usersPath returns the path to users.json
func usersPath() string {
	return filepath.Join(dataDir(), "users.json")
}

// LoadUsers loads the user whitelist
func LoadUsers() []UserEntry {
	if UseRedis() {
		return LoadUsersFromRedis()
	}

	data, err := os.ReadFile(usersPath())
	if err != nil {
		return nil
	}
	var users []UserEntry
	_ = json.Unmarshal(data, &users)
	return users
}

// SaveUsers saves the user whitelist
func SaveUsers(users []UserEntry) error {
	if UseRedis() {
		return SaveUsersToRedis(users)
	}

	data, _ := json.MarshalIndent(users, "", "  ")
	return os.WriteFile(usersPath(), data, 0644)
}

// ValidateUser checks if employee_id and name match the whitelist
func ValidateUser(employeeID, name string) (bool, *UserEntry) {
	users := LoadUsers()
	if len(users) == 0 {
		// No whitelist configured, allow all
		return true, &UserEntry{EmployeeID: employeeID, Name: name}
	}
	for i, u := range users {
		if u.EmployeeID == employeeID {
			if u.Name == name {
				return true, &users[i]
			}
			return false, nil
		}
	}
	return false, nil
}

// IsAdmin checks if employee_id is an admin
func IsAdmin(employeeID string) bool {
	users := LoadUsers()
	for _, u := range users {
		if u.EmployeeID == employeeID && u.IsAdmin {
			return true
		}
	}
	return false
}
