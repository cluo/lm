// Package lm is used as wrapper for lc, redis, mysql etc...
// Created by simplejia [6/2016]
package lm

import (
	"encoding/json"

	"github.com/garyburd/redigo/redis"
	"github.com/simplejia/lc"

	"reflect"
	"time"
)

type McStru struct {
	Expire time.Duration
	Pool   *redis.Pool
}

type LcStru struct {
	Expire time.Duration
	Safety bool // true: 对lc在并发状态下返回的nil值不接受
}

type ft func(p, r interface{}) error
type mft func(p interface{}) string

func GluesMc(ps interface{}, result interface{}, f ft, mf mft, stru *McStru) (err error) {
	expire, pool := stru.Expire, stru.Pool

	rps := reflect.ValueOf(ps)
	num := rps.Len()
	if num == 0 {
		return
	}

	rresult := reflect.Indirect(reflect.ValueOf(result))
	if rresult.IsNil() {
		rresult = reflect.MakeMap(rresult.Type())
		reflect.Indirect(reflect.ValueOf(result)).Set(rresult)
	}

	keys := make([]interface{}, num)
	for i := 0; i < num; i++ {
		rp := rps.Index(i)
		p := rp.Interface()
		key := mf(p)
		keys[i] = key
	}

	conn := pool.Get()
	defer conn.Close()
	vs, err := redis.Strings(conn.Do("MGET", keys...))
	if err != nil {
		return
	}

	rpsNone := reflect.MakeSlice(reflect.TypeOf(ps), 0, 0)
	for i := 0; i < num; i++ {
		rp := rps.Index(i)
		if v := vs[i]; v != "" {
			var rppv reflect.Value
			var pvNew interface{}
			if re := reflect.TypeOf(result).Elem().Elem(); re.Kind() == reflect.Ptr {
				rppv = reflect.New(re.Elem())
				pvNew = rppv.Interface()
			} else {
				rppv = reflect.New(re)
				pvNew = rppv.Interface()
				rppv = reflect.Indirect(rppv)
			}
			if errIgnore := json.Unmarshal([]byte(v), &pvNew); errIgnore != nil {
				// no expected here
				continue
			}
			if pvNew == nil {
				continue
			}
			rresult.SetMapIndex(rp, rppv)
		} else {
			rpsNone = reflect.Append(rpsNone, rp)
			continue
		}
	}

	numNone := rpsNone.Len()
	if numNone == 0 {
		return
	}

	rresultPtrNone := reflect.New(rresult.Type())
	reflect.Indirect(rresultPtrNone).Set(reflect.MakeMap(rresult.Type()))
	rresultNone := reflect.Indirect(rresultPtrNone)
	err = f(rpsNone.Interface(), rresultPtrNone.Interface())
	if err != nil {
		return
	}

	for i := 0; i < rpsNone.Len(); i++ {
		rpNone := rpsNone.Index(i)
		pNone := rpNone.Interface()
		key4mc := mf(pNone)
		expire4mc := int(expire / time.Second)
		if rresultNone.IsValid() {
			rv := rresultNone.MapIndex(rpNone)
			if rv.IsValid() {
				rresult.SetMapIndex(rpNone, rv)
				v, errIgnore := json.Marshal(rv.Interface())
				if errIgnore == nil { // errIgnore is not expected here
					conn.Do("SETEX", key4mc, expire4mc, v)
					continue
				}
			}
		}
		conn.Do("SETEX", key4mc, expire4mc, "null")
	}

	return
}

func GlueMc(p interface{}, result interface{}, f ft, mf mft, stru *McStru) (err error) {
	expire, pool := stru.Expire, stru.Pool

	key4mc := mf(p)
	expire4mc := int(expire / time.Second)

	conn := pool.Get()
	defer conn.Close()
	v, errIgnore := redis.String(conn.Do("GET", key4mc))
	if errIgnore == nil {
		if errIgnore := json.Unmarshal([]byte(v), &result); errIgnore != nil {
			// no expected here
		}
		return
	} else if errIgnore != redis.ErrNil {
		err = errIgnore
		return
	}

	err = f(p, result)
	if err != nil {
		return
	}

	vs, errIgnore := json.Marshal(result)
	if errIgnore == nil { // errIgnore is not expected here
		conn.Do("SETEX", key4mc, expire4mc, vs)
	}
	return
}

func GluesLc(ps interface{}, result interface{}, f ft, mf mft, stru *LcStru) (err error) {
	expire, safety := stru.Expire, stru.Safety

	rps := reflect.ValueOf(ps)
	num := rps.Len()
	if num == 0 {
		return
	}

	rresult := reflect.Indirect(reflect.ValueOf(result))
	if rresult.IsNil() {
		rresult = reflect.MakeMap(rresult.Type())
		reflect.Indirect(reflect.ValueOf(result)).Set(rresult)
	}

	keys, keysM := make([]string, num), map[string]interface{}{}
	for i := 0; i < num; i++ {
		rp := rps.Index(i)
		p := rp.Interface()
		key := mf(p)
		keys[i] = key
		keysM[key] = p
	}

	vsLc, vsAlterLc := lc.Mget(keys)
	for k, v := range vsLc {
		if v == nil {
			if safety {
				delete(vsLc, k)
			}
			continue
		}
		p := keysM[k]
		rresult.SetMapIndex(reflect.ValueOf(p), reflect.ValueOf(v))
	}

	numNone := num - len(vsLc)
	if numNone == 0 {
		return
	}

	rpsNone := reflect.MakeSlice(reflect.TypeOf(ps), 0, numNone)
	for i := 0; i < num; i++ {
		rp := rps.Index(i)
		p := rp.Interface()
		key := mf(p)
		if _, ok := vsLc[key]; !ok {
			rpsNone = reflect.Append(rpsNone, rp)
		}
	}

	rresultPtrNone := reflect.New(rresult.Type())
	reflect.Indirect(rresultPtrNone).Set(reflect.MakeMap(rresult.Type()))
	rresultNone := reflect.Indirect(rresultPtrNone)
	errIgnore := f(rpsNone.Interface(), rresultPtrNone.Interface())
	if errIgnore != nil {
		if safety {
			err = errIgnore
			return
		}
		for k, v := range vsAlterLc {
			if v == nil {
				continue
			}
			p := keysM[k]
			rresult.SetMapIndex(reflect.ValueOf(p), reflect.ValueOf(v))
		}
	}

	for i := 0; i < rpsNone.Len(); i++ {
		rpNone := rpsNone.Index(i)
		pNone := rpNone.Interface()
		key := mf(pNone)
		rv := rresultNone.MapIndex(rpNone)
		if rv.IsValid() {
			rresult.SetMapIndex(rpNone, rv)
			lc.Set(key, rv.Interface(), expire)
			continue
		}
		lc.Set(key, nil, expire)
	}

	return
}

func GlueLc(p interface{}, result interface{}, f ft, mf mft, stru *LcStru) (err error) {
	expire, safety := stru.Expire, stru.Safety

	rresult := reflect.Indirect(reflect.ValueOf(result))

	key := mf(p)
	vLc, ok := lc.Get(key)
	if vLc != nil {
		rresult.Set(reflect.Indirect(reflect.ValueOf(vLc)))
	}
	if ok {
		if vLc != nil || !safety {
			return
		}
	}

	rresultNone := reflect.New(rresult.Type())
	errIgnore := f(p, rresultNone.Interface())
	if errIgnore != nil {
		if safety {
			err = errIgnore
			return
		}
		return
	}

	rresult.Set(reflect.Indirect(rresultNone))
	lc.Set(key, result, expire)
	return
}

func Glues(ps interface{}, result interface{}, f ft, mf mft, mcStru *McStru, lcStru *LcStru) (err error) {
	return GluesLc(
		ps,
		result,
		func(ps, result interface{}) error {
			return GluesMc(
				ps,
				result,
				f,
				mf,
				mcStru,
			)
		},
		mf,
		lcStru,
	)
}

func Glue(p interface{}, result interface{}, f ft, mf mft, mcStru *McStru, lcStru *LcStru) (err error) {
	return GlueLc(
		p,
		result,
		func(p, result interface{}) error {
			return GlueMc(
				p,
				result,
				f,
				mf,
				mcStru,
			)
		},
		mf,
		lcStru,
	)
}