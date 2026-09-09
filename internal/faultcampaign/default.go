package faultcampaign

import "GopherAI/common/mysql"

func NewDefaultService() (*Service, error) { return NewService(NewGormRepository(mysql.DB)) }
