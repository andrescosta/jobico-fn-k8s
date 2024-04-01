package ref

func Of[T any](a T) *T {
	return &a
}

func Ofs[T any](ss []T) []*T {
	r := make([]*T, len(ss))
	for i := range ss {
		r[i] = &ss[i]
	}
	return r
}
