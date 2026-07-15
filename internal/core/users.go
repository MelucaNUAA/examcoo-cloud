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
}

// usersPath returns the path to users.json
func usersPath() string {
	return filepath.Join(dataDir(), "users.json")
}

// LoadUsers loads the user whitelist
func LoadUsers() []UserEntry {
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
	data, _ := json.MarshalIndent(users, "", "  ")
	return os.WriteFile(usersPath(), data, 0644)
}

// ValidateUser checks if employee_id and name match the whitelist
func ValidateUser(employeeID, name string) (bool, string) {
	users := LoadUsers()
	if len(users) == 0 {
		// No whitelist configured, allow all
		return true, ""
	}
	for _, u := range users {
		if u.EmployeeID == employeeID {
			if u.Name == name {
				return true, u.Department
			}
			return false, "姓名不匹配"
		}
	}
	return false, "员工号不在名单中"
}

// UserExists checks if employee_id exists in whitelist
func UserExists(employeeID string) bool {
	users := LoadUsers()
	for _, u := range users {
		if u.EmployeeID == employeeID {
			return true
		}
	}
	return false
}
