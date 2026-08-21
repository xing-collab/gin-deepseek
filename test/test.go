package test

import "fmt"

type User struct {
	Username string
	Password string
}

func NewUser(username string, passord string) (newUser User) {
	return User{
		Username: username,
		Password: passord,
	}
}

func (u *User) Test(newpwd string, fT func(u User) string) error {
	newU := User{
		Username: u.Username,
		Password: newpwd,
	}
	return Test2(newU, func(u User) string { return fT(u) })

}

func Test2(u User, f func(u User) string) error {
	result := f(u)
	fmt.Print("匿名函数调用打印==》" + result)
	return nil
}
