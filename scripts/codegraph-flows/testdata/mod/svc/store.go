package svc

type Store interface {
	Put(id string) error
}

type Memory struct{}

func (Memory) Put(id string) error { return nil }

func (s *Server) Run(st Store) error {
	if err := st.Put("x"); err != nil {
		return err
	}
	return s.Save()
}

func (s *Server) Save() error { return nil }

type Server struct{}
