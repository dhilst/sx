package main

func safe(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = nil
		}
	}()
	f()
	return nil
}
