package profilememory

import "GopherAI/common/mysql"

func NewDefaultService() *Service {
	service, err := NewService(NewGormRepository(mysql.DB), SystemClock{})
	if err != nil {
		panic(err)
	}
	return service
}
