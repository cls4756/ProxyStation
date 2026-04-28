// db 层直接参考 v2rayA 的 bbolt 封装
package db

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

var database *bolt.DB

func Init(path string) error {
	var err error
	database, err = bolt.Open(path, 0600, nil)
	return err
}

func Close() error {
	return database.Close()
}

func Set(bucket, key string, value interface{}) error {
	return database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		if value == nil {
			return b.Delete([]byte(key))
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

func Get(bucket, key string, value interface{}) error {
	return database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("key %s not found", key)
		}
		return json.Unmarshal(data, value)
	})
}

func GetRaw(bucket, key string) ([]byte, error) {
	var result []byte
	err := database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %s not found", bucket)
		}
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("key %s not found", key)
		}
		result = make([]byte, len(data))
		copy(result, data)
		return nil
	})
	return result, err
}

func BucketClear(bucket string) error {
	return database.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket([]byte(bucket)); err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		_, err := tx.CreateBucket([]byte(bucket))
		return err
	})
}

func Exists(bucket, key string) bool {
	var exists bool
	_ = database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		exists = b.Get([]byte(key)) != nil
		return nil
	})
	return exists
}

// ListAppend appends items to a JSON array stored at bucket/key
func ListAppend(bucket, key string, items interface{}) error {
	return database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		var existing []json.RawMessage
		if data := b.Get([]byte(key)); data != nil {
			_ = json.Unmarshal(data, &existing)
		}
		newData, err := json.Marshal(items)
		if err != nil {
			return err
		}
		var newItems []json.RawMessage
		if err := json.Unmarshal(newData, &newItems); err != nil {
			// single item
			newItems = []json.RawMessage{newData}
		}
		existing = append(existing, newItems...)
		data, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

func ListGetAll(bucket, key string) ([][]byte, error) {
	var result [][]byte
	err := database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		for _, item := range items {
			cp := make([]byte, len(item))
			copy(cp, item)
			result = append(result, cp)
		}
		return nil
	})
	return result, err
}

func ListGet(bucket, key string, index int) ([]byte, error) {
	all, err := ListGetAll(bucket, key)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(all) {
		return nil, fmt.Errorf("index %d out of range", index)
	}
	return all[index], nil
}

func ListSet(bucket, key string, index int, value interface{}) error {
	return database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		var existing []json.RawMessage
		if data := b.Get([]byte(key)); data != nil {
			_ = json.Unmarshal(data, &existing)
		}
		if index < 0 || index >= len(existing) {
			return fmt.Errorf("index %d out of range", index)
		}
		newData, err := json.Marshal(value)
		if err != nil {
			return err
		}
		existing[index] = newData
		data, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

func ListRemove(bucket, key string, indexes []int) error {
	return database.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		var existing []json.RawMessage
		if data := b.Get([]byte(key)); data != nil {
			_ = json.Unmarshal(data, &existing)
		}
		idxSet := make(map[int]struct{})
		for _, i := range indexes {
			idxSet[i] = struct{}{}
		}
		var result []json.RawMessage
		for i, item := range existing {
			if _, skip := idxSet[i]; !skip {
				result = append(result, item)
			}
		}
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), data)
	})
}

func ListLen(bucket, key string) (int, error) {
	all, err := ListGetAll(bucket, key)
	return len(all), err
}

func GetBucketLen(bucket string) (int, error) {
	var count int
	err := database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket not found")
		}
		count = b.Stats().KeyN
		return nil
	})
	return count, err
}

func GetBucketKeys(bucket string) ([]string, error) {
	var keys []string
	err := database.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	return keys, err
}
