package evolution

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/failurepool"
)

func NewDefaultService() (*Service, error) {
	return NewService(NewGormRepository(mysql.DB), failurepool.NewGormRepository(mysql.DB))
}
