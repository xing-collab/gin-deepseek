package test

type Chan struct {
	Input  chan<- string
	Output <-chan string
}

func NewChan() Chan {
	ch := make(chan string)
	return Chan{
		Input:  ch,
		Output: ch,
	}
}

func (c *Chan) Send(data string) {
	c.Input <- data
}

func (c *Chan) Get() string {
	date := <-c.Output
	return date
}

func (c *Chan) Close() {
	close(c.Input)
}
