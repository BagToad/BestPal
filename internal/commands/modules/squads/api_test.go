package squads

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSnowflakeAfter(t *testing.T) {
	assert.True(t, snowflakeAfter("10", ""))
	assert.True(t, snowflakeAfter("100", "99"))
	assert.True(t, snowflakeAfter("124", "123"))
	assert.False(t, snowflakeAfter("123", "123"))
	assert.False(t, snowflakeAfter("99", "100"))
}
