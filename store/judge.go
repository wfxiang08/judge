package store

import (
	"encoding/json"
	"fmt"
	"github.com/open-falcon/common/model"
	"github.com/open-falcon/judge/g"
	"log"
)

func Judge(L *SafeLinkedList, firstItem *model.JudgeItem, now int64) {
	CheckStrategy(L, firstItem, now)
	CheckExpression(L, firstItem, now)
}

// 判断的策略是什么呢?
// strategy是按照: <endpoint, metric>来聚类的，不考虑tags
//
func CheckStrategy(L *SafeLinkedList, firstItem *model.JudgeItem, now int64) {
	// firstItem 作为: SafeLinkedList L的第一个元素,

	// Strategy的聚类
	key := fmt.Sprintf("%s/%s", firstItem.Endpoint, firstItem.Metric)
	strategyMap := g.StrategyMap.Get()
	strategies, exists := strategyMap[key]
	if !exists {
		return
	}

	// 策略有自己关联的 Tags
	for _, s := range strategies {
		// 因为key仅仅是endpoint和metric，所以得到的strategies并不一定是与当前judgeItem相关的
		// 比如lg-dinp-docker01.bj配置了两个proc.num的策略，一个name=docker，一个name=agent
		// 所以此处要排除掉一部分
		related := true
		for tagKey, tagVal := range s.Tags {
			// Tags:
			//      du=/home
			//      du=/
			// 如果strategy存在Tags, 则Tags必须精确匹配
			if myVal, exists := firstItem.Tags[tagKey]; !exists || myVal != tagVal {
				related = false
				break
			}
		}

		if !related {
			continue
		}

		judgeItemWithStrategy(L, s, firstItem, now)
	}
}

func judgeItemWithStrategy(L *SafeLinkedList, strategy model.Strategy, firstItem *model.JudgeItem, now int64) {

	// 如何执行一个策略呢?
	// firstItem是  L的第一个元素
	fn, err := ParseFuncFromString(strategy.Func, strategy.Operator, strategy.RightValue)
	if err != nil {
		log.Printf("[ERROR] parse func %s fail: %v. strategy id: %d", strategy.Func, err, strategy.Id)
		return
	}

	// 对于Gauge型的数值
	// HistoryData[0]为firstItem, History[1], ..., 越来越旧
	historyData, leftValue, isTriggered, isEnough := fn.Compute(L)
	if !isEnough {
		return
	}

	// 按照当前时间生成一个Event
	event := &model.Event{
		Id:         fmt.Sprintf("s_%d_%s", strategy.Id, firstItem.PrimaryKey()),
		Strategy:   &strategy,
		Endpoint:   firstItem.Endpoint,
		LeftValue:  leftValue,
		EventTime:  firstItem.Timestamp, // 最后一个Event的时间
		PushedTags: firstItem.Tags,
	}

	// 是否有必要SendEvent呢?
	sendEventIfNeed(historyData, isTriggered, now, event, strategy.MaxStep)
}

func sendEvent(event *model.Event) {
	// update last event
	// 更新最后的Event
	g.LastEvents.Set(event.Id, event)

	bs, err := json.Marshal(event)
	if err != nil {
		log.Printf("json marshal event %v fail: %v", event, err)
		return
	}

	// send to redis
	redisKey := fmt.Sprintf(g.Config().Alarm.QueuePattern, event.Priority())
	rc := g.RedisConnPool.Get()
	defer rc.Close()
	rc.Do("LPUSH", redisKey, string(bs))
}

func CheckExpression(L *SafeLinkedList, firstItem *model.JudgeItem, now int64) {
	keys := buildKeysFromMetricAndTags(firstItem)
	if len(keys) == 0 {
		return
	}

	// expression可能会被多次重复处理，用此数据结构保证只被处理一次
	handledExpression := make(map[int]struct{})

	expressionMap := g.ExpressionMap.Get()
	for _, key := range keys {
		expressions, exists := expressionMap[key]
		if !exists {
			continue
		}

		related := filterRelatedExpressions(expressions, firstItem)
		for _, exp := range related {
			if _, ok := handledExpression[exp.Id]; ok {
				continue
			}
			handledExpression[exp.Id] = struct{}{}
			judgeItemWithExpression(L, exp, firstItem, now)
		}
	}
}

func buildKeysFromMetricAndTags(item *model.JudgeItem) (keys []string) {
	for k, v := range item.Tags {
		keys = append(keys, fmt.Sprintf("%s/%s=%s", item.Metric, k, v))
	}
	keys = append(keys, fmt.Sprintf("%s/endpoint=%s", item.Metric, item.Endpoint))
	return
}

func filterRelatedExpressions(expressions []*model.Expression, firstItem *model.JudgeItem) []*model.Expression {
	size := len(expressions)
	if size == 0 {
		return []*model.Expression{}
	}

	exps := make([]*model.Expression, 0, size)

	for _, exp := range expressions {

		related := true

		itemTagsCopy := firstItem.Tags
		// 注意：exp.Tags 中可能会有一个endpoint=xxx的tag
		if _, ok := exp.Tags["endpoint"]; ok {
			itemTagsCopy = copyItemTags(firstItem)
		}

		for tagKey, tagVal := range exp.Tags {
			if myVal, exists := itemTagsCopy[tagKey]; !exists || myVal != tagVal {
				related = false
				break
			}
		}

		if !related {
			continue
		}

		exps = append(exps, exp)
	}

	return exps
}

func copyItemTags(item *model.JudgeItem) map[string]string {
	ret := make(map[string]string)
	ret["endpoint"] = item.Endpoint
	if item.Tags != nil && len(item.Tags) > 0 {
		for k, v := range item.Tags {
			ret[k] = v
		}
	}
	return ret
}

func judgeItemWithExpression(L *SafeLinkedList, expression *model.Expression, firstItem *model.JudgeItem, now int64) {
	fn, err := ParseFuncFromString(expression.Func, expression.Operator, expression.RightValue)
	if err != nil {
		log.Printf("[ERROR] parse func %s fail: %v. expression id: %d", expression.Func, err, expression.Id)
		return
	}

	historyData, leftValue, isTriggered, isEnough := fn.Compute(L)
	if !isEnough {
		return
	}

	event := &model.Event{
		Id:         fmt.Sprintf("e_%d_%s", expression.Id, firstItem.PrimaryKey()),
		Expression: expression,
		Endpoint:   firstItem.Endpoint,
		LeftValue:  leftValue,
		EventTime:  firstItem.Timestamp,
		PushedTags: firstItem.Tags,
	}

	sendEventIfNeed(historyData, isTriggered, now, event, expression.MaxStep)

}

func sendEventIfNeed(historyData []*model.HistoryData, isTriggered bool,
	now int64, event *model.Event, maxStep int) {

	lastEvent, exists := g.LastEvents.Get(event.Id)

	// 如果异常出现
	if isTriggered {
		event.Status = "PROBLEM"
		// 1. 本次触发了阈值，之前又没报过警，得产生一个报警Event
		if !exists || lastEvent.Status[0] == 'O' {

			event.CurrentStep = 1

			// 但是有些用户把最大报警次数配置成了0，相当于屏蔽了，要检查一下
			if maxStep == 0 {
				return
			}

			sendEvent(event)
			return
		}

		// 2. 逻辑走到这里，说明之前Event是PROBLEM状态
		if lastEvent.CurrentStep >= maxStep {
			// 报警次数已经足够多，到达了最多报警次数了，不再报警
			return
		}

		// 3. 可以发送Event, 但是需要考虑其他情况

		// historyData记录了最近一段时间内每一个点的状态，不管有没有Event生成
		if historyData[len(historyData)-1].Timestamp <= lastEvent.EventTime {
			// 4. 产生过报警的点，就不能再使用来判断了，否则容易出现一分钟报一次的情况
			// 只需要拿最后一个historyData来做判断即可，因为它的时间最老
			return
		}

		// 5. 默认大概: 300s报警一次
		if now-lastEvent.EventTime < g.Config().Alarm.MinInterval {
			// 报警不能太频繁，两次报警之间至少要间隔MinInterval秒，否则就不能报警
			return
		}

		// 6. 终于可以报警了
		event.CurrentStep = lastEvent.CurrentStep + 1
		sendEvent(event)
	} else {
		// 如果LastEvent是Problem，报OK，否则啥都不做
		// 从 Problem --> OK, 通知服务正常
		if exists && lastEvent.Status[0] == 'P' {
			event.Status = "OK"
			event.CurrentStep = 1
			sendEvent(event)
		}
	}
}
