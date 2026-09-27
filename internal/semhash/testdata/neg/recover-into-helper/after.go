package main

func safe(f func()) (err error) {
	defer handle(&err)
	f()
	return nil
}

func handle(err *error) {
	if r := recover(); r != nil {
		*err = nil
	}
}
