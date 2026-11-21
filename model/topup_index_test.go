package model

import (
	"fmt"
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// TestTopUpUserCreateTimeIndexMatrix verifies the (user_id, create_time)
// composite ordering index (v1.1.0 M1 · B07) across supported databases.
// The user top-up list always filters by 「user_id = ? AND create_time >= ?」
// (model/topup.go), so column order matters: equality column first.
func TestTopUpUserCreateTimeIndexMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "topup_index_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })

			if dialect == "postgres" {
				// Isolate each dialect run in its own schema so concurrent
				// postgres test databases cannot see each other's tables.
				db = db.Begin()
				require.NoError(t, db.Error)
				t.Cleanup(func() { assert.NoError(t, db.Rollback().Error) })
				schemaName := fmt.Sprintf("topup_index_test_%d", os.Getpid())
				require.NoError(t, db.Exec("CREATE SCHEMA IF NOT EXISTS ?", clause.Table{Name: schemaName}).Error)
				require.NoError(t, db.Exec("SET LOCAL search_path TO ?", clause.Table{Name: schemaName}).Error)
			} else {
				t.Cleanup(func() { assert.NoError(t, db.Migrator().DropTable(&TopUp{})) })
			}

			require.NoError(t, db.AutoMigrate(&TopUp{}))
			indexes, err := db.Migrator().GetIndexes(&TopUp{})
			require.NoError(t, err)
			byName := make(map[string]gorm.Index, len(indexes))
			for _, index := range indexes {
				byName[index.Name()] = index
			}
			index, ok := byName["idx_top_ups_user_create_time"]
			require.True(t, ok, "migration must create the top-up ordering index")
			var columns []string
			if dialect == "postgres" {
				// GORM's PostgreSQL GetIndexes does not preserve index key order.
				require.NoError(t, db.Raw(`
SELECT attribute.attname
FROM pg_catalog.pg_index AS index_meta
CROSS JOIN LATERAL unnest(index_meta.indkey) WITH ORDINALITY AS index_column(attnum, ordinal_position)
JOIN pg_catalog.pg_attribute AS attribute
  ON attribute.attrelid = index_meta.indrelid AND attribute.attnum = index_column.attnum
WHERE index_meta.indexrelid = to_regclass(?)
ORDER BY index_column.ordinal_position
`, "topup_index_test_"+index.Name()).Scan(&columns).Error)
			} else {
				columns = index.Columns()
			}
			assert.Equal(t, []string{"user_id", "create_time"}, columns)

			// Idempotency: re-running AutoMigrate must not error out.
			require.NoError(t, db.AutoMigrate(&TopUp{}))
			indexesAgain, err := db.Migrator().GetIndexes(&TopUp{})
			require.NoError(t, err)
			assert.Len(t, indexesAgain, len(indexes), "re-migration must not duplicate indexes")

			// The single-column user_id index must survive alongside the
			// composite one: other call sites filter by user_id alone. GORM
			// auto-names it with the (prefixed) table name, so match by
			// columns instead of hard-coding the generated name.
			var singleUserIndexes int
			for name, candidate := range byName {
				if name == "idx_top_ups_user_create_time" {
					continue
				}
				if columns := candidate.Columns(); len(columns) == 1 && columns[0] == "user_id" {
					singleUserIndexes++
				}
			}
			assert.Equal(t, 1, singleUserIndexes, "single-column user_id index must remain")
		})
	}
}
