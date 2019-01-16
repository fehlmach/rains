// Code generated by "stringer -type=Hash"; DO NOT EDIT.

package algorithmTypes

import "strconv"

const _Hash_name = "NoHashAlgoSha256Sha384Sha512Shake256Fnv64Fnv128"

var _Hash_index = [...]uint8{0, 10, 16, 22, 28, 36, 41, 47}

func (i Hash) String() string {
	if i < 0 || i >= Hash(len(_Hash_index)-1) {
		return "Hash(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Hash_name[_Hash_index[i]:_Hash_index[i+1]]
}