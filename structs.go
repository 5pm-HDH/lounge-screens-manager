package main

type LanSwitchResponse struct {
	Result LanSwitchResult `json:"result"`
}

type LanSwitchResult struct {
	Id     string `json:"id"`
	Error  int    `json:"error"`
	Status int    `json:"status"`
	Set    string `json:"set"`
}
