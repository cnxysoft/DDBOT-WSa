package crypto

// CRC32 implements a JavaScript-style CRC32 variant
type CRC32 struct {
	Table []uint32
	Mask  uint32
	Poly  uint32
}

// NewCRC32 creates a new CRC32 instance
func NewCRC32() *CRC32 {
	c := &CRC32{
		Mask: 0xFFFFFFFF,
		Poly: 0xEDB88320,
	}
	c.initTable()
	return c
}

// initTable initializes the CRC32 lookup table
func (c *CRC32) initTable() {
	c.Table = make([]uint32, 256)
	for d := 0; d < 256; d++ {
		r := uint32(d)
		for i := 0; i < 8; i++ {
			if (r & 1) != 0 {
				r = (r >> 1) ^ c.Poly
			} else {
				r >>= 1
			}
			r &= c.Mask
		}
		c.Table[d] = r
	}
}

// crc32Core computes the core CRC32 state
func (c *CRC32) crc32Core(data []byte) uint32 {
	cc := c.Mask
	for _, b := range data {
		cc = c.Table[(cc&0xFF)^uint32(b)] ^ (cc >> 8)
	}
	return cc & c.Mask
}

// toSigned32 converts an unsigned 32-bit int to a signed 32-bit value
func (c *CRC32) toSigned32(u uint32) int32 {
	if (u & 0x80000000) != 0 {
		return -int32(0x100000000 - uint64(u))
	}
	return int32(u)
}

// CRC32JSInt implements JavaScript-style CRC32
// Formula: (-1 ^ c ^ 0xEDB88320) >>> 0
func (c *CRC32) CRC32JSInt(data []byte, signed bool) int {
	cc := c.crc32Core(data)
	a := c.Poly
	u := (c.Mask ^ cc ^ a) & c.Mask
	if signed {
		return int(c.toSigned32(u))
	}
	return int(u)
}

// CRC32JSIntStatic is a convenience wrapper using a static CRC32 instance
var staticCRC32 = NewCRC32()

// CRC32JSIntStatic is a static helper for CRC32 calculation
func CRC32JSInt(data []byte) int {
	return staticCRC32.CRC32JSInt(data, true)
}