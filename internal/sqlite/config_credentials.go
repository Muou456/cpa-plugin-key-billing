package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"cpa-key-billing/internal/billing"
)

func replaceConfigCredentials(tx *sql.Tx, state *billing.State) error {
	if _, err := tx.Exec("DELETE FROM config_credentials"); err != nil {
		return fmt.Errorf("Save configured credentials: %w", err)
	}
	for ref, credential := range state.ConfigCredentials {
		balance, err := json.Marshal(credential.Balance)
		if err != nil {
			return fmt.Errorf("Encode credential balance: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO config_credentials(ref, provider, key_preview, disabled, auth_index, type, name, base_url, priority, weight, balance_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			ref, credential.Provider, credential.KeyPreview, credential.Disabled, credential.AuthIndex, credential.Type, credential.Name, credential.BaseURL, credential.Priority, credential.Weight, string(balance)); err != nil {
			return fmt.Errorf("Save configured credentials: %w", err)
		}
	}
	return nil
}

func (d *DB) loadConfigCredentials(state *billing.State) error {
	rows, err := d.db.Query("SELECT ref, provider, key_preview, disabled, auth_index, type, name, base_url, priority, weight, balance_json FROM config_credentials")
	if err != nil {
		return fmt.Errorf("Read configured credentials: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref, balance string
		var credential billing.ConfigCredential
		if err := rows.Scan(&ref, &credential.Provider, &credential.KeyPreview, &credential.Disabled, &credential.AuthIndex, &credential.Type, &credential.Name, &credential.BaseURL, &credential.Priority, &credential.Weight, &balance); err != nil {
			return fmt.Errorf("Read configured credentials: %w", err)
		}
		if err := json.Unmarshal([]byte(balance), &credential.Balance); err != nil {
			return fmt.Errorf("Read credential balance: %w", err)
		}
		state.ConfigCredentials[ref] = credential
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("Read configured credentials: %w", err)
	}
	return nil
}
