package core

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client
var ctx = context.Background()

// InitStorage initializes Redis connection if REDIS_URL is set
func InitStorage() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		log.Println("REDIS_URL not set, using file storage")
		return
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("Failed to parse REDIS_URL: %v, using file storage", err)
		return
	}

	rdb = redis.NewClient(opt)

	// Test connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("Failed to connect to Redis: %v, using file storage", err)
		rdb = nil
		return
	}

	log.Println("Connected to Redis successfully")

	// Initialize default admin if users list is empty
	initDefaultUsers()
}

// initDefaultUsers creates default admin user if no users exist
func initDefaultUsers() {
	users := LoadUsersFromRedis()
	if len(users) > 0 {
		return
	}

	defaultUsers := []UserEntry{
		{EmployeeID: "admin", Name: "管理员", Department: "管理部", IsAdmin: true},
	}

	if err := SaveUsersToRedis(defaultUsers); err != nil {
		log.Printf("Failed to create default users: %v", err)
		return
	}

	log.Println("Created default admin user (admin/管理员)")
}

// UseRedis returns true if Redis is available
func UseRedis() bool {
	return rdb != nil
}

// ── Config Storage ──

func redisConfigKey(employeeID string) string {
	return "config:" + employeeID
}

// LoadUserConfigFromRedis loads user config from Redis
func LoadUserConfigFromRedis(employeeID string) Config {
	cfg := DefaultConfig
	data, err := rdb.Get(ctx, redisConfigKey(employeeID)).Bytes()
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)

	// Decrypt API Key
	if cfg.APIKey != "" {
		decrypted, err := Decrypt(cfg.APIKey)
		if err == nil {
			cfg.APIKey = decrypted
		}
	}

	return cfg
}

// SaveUserConfigToRedis saves user config to Redis
func SaveUserConfigToRedis(employeeID string, cfg Config) error {
	// Encrypt API Key before saving
	if cfg.APIKey != "" {
		encrypted, err := Encrypt(cfg.APIKey)
		if err != nil {
			return err
		}
		cfg.APIKey = encrypted
	}

	data, _ := json.Marshal(cfg)
	return rdb.Set(ctx, redisConfigKey(employeeID), data, 0).Err()
}

// ── Bank Storage ──

const redisBankKey = "bank:all"

// LoadBankFromRedis loads bank from Redis
func LoadBankFromRedis() map[string]BankEntry {
	bank := map[string]BankEntry{}
	data, err := rdb.Get(ctx, redisBankKey).Bytes()
	if err != nil {
		return bank
	}
	_ = json.Unmarshal(data, &bank)
	return bank
}

// SaveBankToRedis saves bank to Redis
func SaveBankToRedis(bank map[string]BankEntry) error {
	data, _ := json.Marshal(bank)
	return rdb.Set(ctx, redisBankKey, data, 0).Err()
}

// ── Users Storage ──

const redisUsersKey = "users:list"

// LoadUsersFromRedis loads users from Redis
func LoadUsersFromRedis() []UserEntry {
	data, err := rdb.Get(ctx, redisUsersKey).Bytes()
	if err != nil {
		return nil
	}
	var users []UserEntry
	_ = json.Unmarshal(data, &users)
	return users
}

// SaveUsersToRedis saves users to Redis
func SaveUsersToRedis(users []UserEntry) error {
	data, _ := json.Marshal(users)
	return rdb.Set(ctx, redisUsersKey, data, 0).Err()
}

// ── Generic helpers ──

// CacheSet sets a key-value pair with expiration
func CacheSet(key string, value interface{}, expiration time.Duration) error {
	if rdb == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	return rdb.Set(ctx, key, data, expiration).Err()
}

// CacheGet gets a value by key
func CacheGet(key string, dest interface{}) error {
	if rdb == nil {
		return redis.Nil
	}
	data, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}
