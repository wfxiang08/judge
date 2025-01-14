package store

import (
	"fmt"
	"github.com/open-falcon/common/model"
	"math"
	"strconv"
	"strings"
)

// 各种报警触发函数
// http://book.open-falcon.com/zh/usage/func.html
//
type Function interface {
	Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool)
}

type MaxFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

func (this MaxFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	// 1. 如何执行指定的策略?
	// 获取一定数量的数据, 例如: all(#3) sum(#3) avg(#10) diff(#10)
	// Limit就是其中的 3, 3, 10, 10等
	vs, isEnough = L.HistoryData(this.Limit)
	if !isEnough {
		return
	}

	// 2. 取HisotryData中最大的value
	//  max(#) 对于最新的3个点，其最大值满足阈值条件则报警
	// 选择其中最大的一个点
	max := vs[0].Value
	for i := 1; i < this.Limit; i++ {
		if max < vs[i].Value {
			max = vs[i].Value
		}
	}

	leftValue = max
	isTriggered = checkIsTriggered(leftValue, this.Operator, this.RightValue)
	return
}

type MinFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

func (this MinFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	vs, isEnough = L.HistoryData(this.Limit)
	if !isEnough {
		return
	}

	// 最小的三个点
	min := vs[0].Value
	for i := 1; i < this.Limit; i++ {
		if min > vs[i].Value {
			min = vs[i].Value
		}
	}

	leftValue = min
	isTriggered = checkIsTriggered(leftValue, this.Operator, this.RightValue)
	return
}

type AllFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

func (this AllFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	vs, isEnough = L.HistoryData(this.Limit)
	if !isEnough {
		return
	}

	// 所有的
	isTriggered = true
	for i := 0; i < this.Limit; i++ {
		isTriggered = checkIsTriggered(vs[i].Value, this.Operator, this.RightValue)
		if !isTriggered {
			break
		}
	}

	leftValue = vs[0].Value
	return
}

type SumFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

func (this SumFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	vs, isEnough = L.HistoryData(this.Limit)
	if !isEnough {
		return
	}

	// 求和
	sum := 0.0
	for i := 0; i < this.Limit; i++ {
		sum += vs[i].Value
	}

	leftValue = sum
	isTriggered = checkIsTriggered(leftValue, this.Operator, this.RightValue)
	return
}

type AvgFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

func (this AvgFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	vs, isEnough = L.HistoryData(this.Limit)
	if !isEnough {
		return
	}

	sum := 0.0
	for i := 0; i < this.Limit; i++ {
		sum += vs[i].Value
	}

	leftValue = sum / float64(this.Limit)
	isTriggered = checkIsTriggered(leftValue, this.Operator, this.RightValue)
	return
}

type DiffFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

// 只要有一个点的diff触发阈值，就报警
func (this DiffFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	// 此处this.Limit要+1，因为通常说diff(#3)，是当前点与历史的3个点相比较
	// 然而最新点已经在linkedlist的第一个位置，所以……
	vs, isEnough = L.HistoryData(this.Limit + 1)
	if !isEnough {
		return
	}

	if len(vs) == 0 {
		isEnough = false
		return
	}

	first := vs[0].Value
	//只要有一个Diff
	isTriggered = false
	for i := 1; i < this.Limit+1; i++ {
		// diff是当前值减去历史值
		leftValue = first - vs[i].Value
		isTriggered = checkIsTriggered(leftValue, this.Operator, this.RightValue)
		if isTriggered {
			break
		}
	}

	return
}

// pdiff(#3)
type PDiffFunction struct {
	Function
	Limit      int
	Operator   string
	RightValue float64
}

func (this PDiffFunction) Compute(L *SafeLinkedList) (vs []*model.HistoryData, leftValue float64, isTriggered bool, isEnough bool) {
	vs, isEnough = L.HistoryData(this.Limit + 1)
	if !isEnough {
		return
	}

	if len(vs) == 0 {
		isEnough = false
		return
	}

	first := vs[0].Value

	isTriggered = false
	for i := 1; i < this.Limit+1; i++ {
		if vs[i].Value == 0 {
			continue
		}

		// 当前值和之前的某个点的差异的: Percent
		leftValue = (first - vs[i].Value) / vs[i].Value * 100.0
		isTriggered = checkIsTriggered(leftValue, this.Operator, this.RightValue)
		if isTriggered {
			break
		}
	}

	return
}

// @str: e.g. all(#3) sum(#3) avg(#10) diff(#10)
func ParseFuncFromString(str string, operator string, rightValue float64) (fn Function, err error) {
	idx := strings.Index(str, "#")
	limit, err := strconv.ParseInt(str[idx+1:len(str)-1], 10, 64)
	if err != nil {
		return nil, err
	}

	switch str[:idx-1] {
	case "max":
		fn = &MaxFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	case "min":
		fn = &MinFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	case "all":
		fn = &AllFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	case "sum":
		fn = &SumFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	case "avg":
		fn = &AvgFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	case "diff":
		fn = &DiffFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	case "pdiff":
		fn = &PDiffFunction{Limit: int(limit), Operator: operator, RightValue: rightValue}
	default:
		err = fmt.Errorf("not_supported_method")
	}

	return
}

// 判断 left, right 是否满足 operator 意义上的关系
func checkIsTriggered(leftValue float64, operator string, rightValue float64) (isTriggered bool) {
	switch operator {
	case "=", "==":
		isTriggered = math.Abs(leftValue-rightValue) < 0.0001
	case "!=":
		isTriggered = math.Abs(leftValue-rightValue) > 0.0001
	case "<":
		isTriggered = leftValue < rightValue
	case "<=":
		isTriggered = leftValue <= rightValue
	case ">":
		isTriggered = leftValue > rightValue
	case ">=":
		isTriggered = leftValue >= rightValue
	}

	return
}
