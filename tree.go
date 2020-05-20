// Copyright (C) 2015 Alex Sergeyev
// This project is licensed under the terms of the MIT license.
// Read LICENSE file for information for all notices and permissions.

package nradix

import (
	"bytes"
	"errors"
	"net"
)

type node struct {
	left, right, parent *node
	value               interface{}
}

// Tree implements radix tree for working with IP/mask. Thread safety is not guaranteed, you should choose your own style of protecting safety of operations.
type Tree struct {
	root *node
	free *node

	alloc []node
}

const (
	startbit  = uint32(0x80000000)
	startbyte = byte(0x80)
)

type OptWalk uint32

const (
	OptWalkIPv4   = OptWalk(0x1)
	OptWalkIPv6   = OptWalk(0x2)
	OptWalkIPAuto = OptWalk(0x3)
)

type findWhat int

const (
	findBest findWhat = iota + 1
	findExact
	findAll
)

var (
	ErrNodeBusy = errors.New("Node Busy")
	ErrNotFound = errors.New("No Such Node")
	ErrBadIP    = errors.New("Bad IP address or mask")
)

// NewTree creates Tree and preallocates (if preallocate not zero) number of nodes that would be ready to fill with data.
func NewTree(preallocate int) *Tree {
	tree := new(Tree)
	tree.root = tree.newnode()
	if preallocate == 0 {
		return tree
	}

	// Simplification, static preallocate max 6 bits
	if preallocate > 6 || preallocate < 0 {
		preallocate = 6
	}

	var key, mask uint32

	for inc := startbit; preallocate > 0; inc, preallocate = inc>>1, preallocate-1 {
		key = 0
		mask >>= 1
		mask |= startbit

		for {
			tree.insert32(key, mask, nil, false)
			key += inc
			if key == 0 { // magic bits collide
				break
			}
		}
	}

	return tree
}

// AddCIDR adds value associated with IP/mask to the tree. Will return error for invalid CIDR or if value already exists.
func (tree *Tree) AddCIDR(cidr string, val interface{}) error {
	return tree.AddCIDRb([]byte(cidr), val)
}

func (tree *Tree) AddCIDRb(cidr []byte, val interface{}) error {
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return err
		}
		return tree.insert32(ip, mask, val, false)
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil {
		return err
	}
	return tree.insert(ip, mask, val, false)
}

// AddCIDR adds value associated with IP/mask to the tree. Will return error for invalid CIDR or if value already exists.
func (tree *Tree) SetCIDR(cidr string, val interface{}) error {
	return tree.SetCIDRb([]byte(cidr), val)
}

func (tree *Tree) SetCIDRb(cidr []byte, val interface{}) error {
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return err
		}
		return tree.insert32(ip, mask, val, true)
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil {
		return err
	}
	return tree.insert(ip, mask, val, true)
}

// DeleteWholeRangeCIDR removes all values associated with IPs
// in the entire subnet specified by the CIDR.
func (tree *Tree) DeleteWholeRangeCIDR(cidr string) error {
	return tree.DeleteWholeRangeCIDRb([]byte(cidr))
}

func (tree *Tree) DeleteWholeRangeCIDRb(cidr []byte) error {
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return err
		}
		return tree.delete32(ip, mask, true)
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil {
		return err
	}
	return tree.delete(ip, mask, true)
}

// DeleteCIDR removes value associated with IP/mask from the tree.
func (tree *Tree) DeleteCIDR(cidr string) error {
	return tree.DeleteCIDRb([]byte(cidr))
}

func (tree *Tree) DeleteCIDRb(cidr []byte) error {
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return err
		}
		return tree.delete32(ip, mask, false)
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil {
		return err
	}
	return tree.delete(ip, mask, false)
}

// FindCIDR traverses tree to proper Node and returns previously saved information in longest covered IP.
func (tree *Tree) FindCIDR(cidr string) (interface{}, error) {
	return tree.FindCIDRb([]byte(cidr))
}

func (tree *Tree) FindCIDRb(cidr []byte) (interface{}, error) {
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return nil, err
		}
		values := tree.find32(ip, mask, findBest)
		if len(values) > 0 {
			return values[0], nil
		}
		return nil, nil
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil || ip == nil {
		return nil, err
	}
	values := tree.find(ip, mask, findBest)
	if len(values) > 0 {
		return values[0], nil
	} else {
		return nil, nil
	}
}

// FindExactCIDR traverses tree to proper Node and returns previously saved information for an exact match.
func (tree *Tree) FindExactCIDR(cidr string) (interface{}, error) {
	return tree.FindExactCIDRb([]byte(cidr))
}

func (tree *Tree) FindExactCIDRb(cidr []byte) (interface{}, error) {
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return nil, err
		}
		values := tree.find32(ip, mask, findExact)
		if len(values) > 0 {
			return values[0], nil
		}
		return nil, ErrNotFound
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil || ip == nil {
		return nil, err
	}
	values := tree.find(ip, mask, findExact)
	if len(values) > 0 {
		return values[0], nil
	}
	return nil, ErrNotFound
}

// FindAllCIDR traverses tree to proper Node and returns previously saved information in all covered IPs.
func (tree *Tree) FindAllCIDR(cidr string) ([]interface{}, error) {
	return tree.FindAllCIDRb([]byte(cidr))
}

func (tree *Tree) FindAllCIDRb(cidr []byte) ([]interface{}, error) {
	var ret []interface{}
	if bytes.IndexByte(cidr, '.') > 0 {
		ip, mask, err := parsecidr4(cidr)
		if err != nil {
			return nil, err
		}
		ret = append(ret, tree.find32(ip, mask, findAll)...)
		return ret, nil
	}
	ip, mask, err := parsecidr6(cidr)
	if err != nil || ip == nil {
		return nil, err
	}
	ret = append(ret, tree.find(ip, mask, findAll)...)
	return ret, nil
}

type WalkTreeFunc func(cidr net.IPNet, value interface{}) error

// WalkTree walks the tree (depth first) and calls the `WalkTreeFunc` for each node with a value.
func (tree *Tree) WalkTree(opt OptWalk, wtfunc WalkTreeFunc) error {
	walkpath := make([]byte, 0, 128)
	return tree.walk(opt, wtfunc, walkpath, tree.root)
}

func (tree *Tree) walk(opt OptWalk, wtfunc WalkTreeFunc, walkpath []byte, node *node) error {
	if node.value != nil {
		ipnet := walkpath2net(opt, walkpath)
		if err := wtfunc(ipnet, node.value); err != nil {
			return err
		}
	}
	if node.left != nil {
		if err := tree.walk(opt, wtfunc, append(walkpath, byte(0)), node.left); err != nil {
			return err
		}
	}
	if node.right != nil {
		if err := tree.walk(opt, wtfunc, append(walkpath, byte(1)), node.right); err != nil {
			return err
		}
	}
	return nil
}

// Takes a walkpath byte slice (0=left, 1=right) and turns it into the net.IPNet that it represents.
func walkpath2net(opt OptWalk, walkpath []byte) net.IPNet {
	ip := make([]byte, 0, net.IPv6len)
	var byteval, bitval byte
	for bit := 0; bit < len(walkpath); bit++ {
		if bit%8 == 0 {
			if bit > 0 {
				ip = append(ip, byteval)
			}
			byteval = 0
			bitval = 0x80
		}
		if walkpath[bit] != 0 {
			byteval |= bitval
		}
		bitval >>= 1
	}
	ip = append(ip, byteval)
	switch {
	case opt&OptWalkIPv4 != 0 && len(ip) <= net.IPv4len:
		mask := net.CIDRMask(len(walkpath), net.IPv4len*8)
		for len(ip) < net.IPv4len {
			ip = append(ip, byte(0))
		}
		return net.IPNet{net.IP(ip), mask}
	case opt&OptWalkIPv6 != 0 && len(ip) <= net.IPv6len:
		mask := net.CIDRMask(len(walkpath), net.IPv6len*8)
		for len(ip) < net.IPv6len {
			ip = append(ip, byte(0))
		}
		return net.IPNet{net.IP(ip), mask}
	}
	return net.IPNet{}
}

func (tree *Tree) insert32(key, mask uint32, value interface{}, overwrite bool) error {
	bit := startbit
	node := tree.root
	next := tree.root
	for bit&mask != 0 {
		if key&bit != 0 {
			next = node.right
		} else {
			next = node.left
		}
		if next == nil {
			break
		}
		bit = bit >> 1
		node = next
	}
	if next != nil {
		if node.value != nil && !overwrite {
			return ErrNodeBusy
		}
		node.value = value
		return nil
	}
	for bit&mask != 0 {
		next = tree.newnode()
		next.parent = node
		if key&bit != 0 {
			node.right = next
		} else {
			node.left = next
		}
		bit >>= 1
		node = next
	}
	node.value = value

	return nil
}

func (tree *Tree) insert(key net.IP, mask net.IPMask, value interface{}, overwrite bool) error {
	if len(key) != len(mask) {
		return ErrBadIP
	}

	var i int
	bit := startbyte
	node := tree.root
	next := tree.root
	for bit&mask[i] != 0 {
		if key[i]&bit != 0 {
			next = node.right
		} else {
			next = node.left
		}
		if next == nil {
			break
		}

		node = next

		if bit >>= 1; bit == 0 {
			if i++; i == len(key) {
				break
			}
			bit = startbyte
		}

	}
	if next != nil {
		if node.value != nil && !overwrite {
			return ErrNodeBusy
		}
		node.value = value
		return nil
	}

	for bit&mask[i] != 0 {
		next = tree.newnode()
		next.parent = node
		if key[i]&bit != 0 {
			node.right = next
		} else {
			node.left = next
		}
		node = next
		if bit >>= 1; bit == 0 {
			if i++; i == len(key) {
				break
			}
			bit = startbyte
		}
	}
	node.value = value

	return nil
}

func (tree *Tree) delete32(key, mask uint32, wholeRange bool) error {
	bit := startbit
	node := tree.root
	for node != nil && bit&mask != 0 {
		if key&bit != 0 {
			node = node.right
		} else {
			node = node.left
		}
		bit >>= 1
	}
	if node == nil {
		return ErrNotFound
	}

	if !wholeRange && (node.right != nil || node.left != nil) {
		// keep it just trim value
		if node.value != nil {
			node.value = nil
			return nil
		}
		return ErrNotFound
	}

	// need to trim leaf
	for {
		if node.parent.right == node {
			node.parent.right = nil
		} else {
			node.parent.left = nil
		}
		// reserve this node for future use
		node.right = tree.free
		tree.free = node
		// move to parent, check if it's free of value and children
		node = node.parent
		if node.right != nil || node.left != nil || node.value != nil {
			break
		}
		// do not delete root node
		if node.parent == nil {
			break
		}
	}

	return nil
}

func (tree *Tree) delete(key net.IP, mask net.IPMask, wholeRange bool) error {
	if len(key) != len(mask) {
		return ErrBadIP
	}

	var i int
	bit := startbyte
	node := tree.root
	for node != nil && bit&mask[i] != 0 {
		if key[i]&bit != 0 {
			node = node.right
		} else {
			node = node.left
		}
		if bit >>= 1; bit == 0 {
			if i++; i == len(key) {
				break
			}
			bit = startbyte
		}
	}
	if node == nil {
		return ErrNotFound
	}

	if !wholeRange && (node.right != nil || node.left != nil) {
		// keep it just trim value
		if node.value != nil {
			node.value = nil
			return nil
		}
		return ErrNotFound
	}

	// need to trim leaf
	for {
		if node.parent.right == node {
			node.parent.right = nil
		} else {
			node.parent.left = nil
		}
		// reserve this node for future use
		node.right = tree.free
		tree.free = node

		// move to parent, check if it's free of value and children
		node = node.parent
		if node.right != nil || node.left != nil || node.value != nil {
			break
		}
		// do not delete root node
		if node.parent == nil {
			break
		}
	}

	return nil
}

func (tree *Tree) find32(key, mask uint32, what findWhat) []interface{} {
	var ret []interface{}
	var exact bool
	bit := startbit
	node := tree.root
	for node != nil {
		if node.value != nil {
			if what == findAll {
				ret = append(ret, node.value)
			} else {
				ret = append(ret[:0], node.value)
			}
			exact = (mask&bit == 0)
		}
		if mask&bit == 0 {
			break
		}
		if key&bit != 0 {
			node = node.right
		} else {
			node = node.left
		}
		bit >>= 1
	}
	if !exact && what == findExact {
		return nil
	}
	return ret
}

func (tree *Tree) find(key net.IP, mask net.IPMask, what findWhat) []interface{} {
	if len(key) != len(mask) {
		return nil
	}
	var ret []interface{}
	var exact bool
	var i int
	bit := startbyte
	node := tree.root
	for node != nil {
		if node.value != nil {
			if what == findAll {
				ret = append(ret, node.value)
			} else {
				ret = append(ret[:0], node.value)
			}
			exact = mask[i]&bit == 0
		}
		if mask[i]&bit == 0 {
			break
		}
		if key[i]&bit != 0 {
			node = node.right
		} else {
			node = node.left
		}
		if bit >>= 1; bit == 0 {
			i, bit = i+1, startbyte
			if i >= len(key) {
				// reached depth of the tree, there should be matching node...
				if node != nil {
					if what == findAll {
						ret = append(ret, node.value)
					} else {
						ret = append(ret[:0], node.value)
					}
					exact = (node.value != nil)
				}
				break
			}
		}
	}
	if !exact && what == findExact {
		return nil
	}
	return ret
}

func (tree *Tree) newnode() (p *node) {
	if tree.free != nil {
		p = tree.free
		tree.free = tree.free.right

		// release all prior links
		p.right = nil
		p.parent = nil
		p.left = nil
		p.value = nil
		return p
	}

	ln := len(tree.alloc)
	if ln == cap(tree.alloc) {
		// filled one row, make bigger one
		tree.alloc = make([]node, ln+200)[:1] // 200, 600, 1400, 3000, 6200, 12600 ...
		ln = 0
	} else {
		tree.alloc = tree.alloc[:ln+1]
	}
	return &(tree.alloc[ln])
}

func loadip4(ipstr []byte) (uint32, error) {
	var (
		ip  uint32
		oct uint32
		b   byte
		num byte
	)

	for _, b = range ipstr {
		switch {
		case b == '.':
			num++
			if 0xffffffff-ip < oct {
				return 0, ErrBadIP
			}
			ip = ip<<8 + oct
			oct = 0
		case b >= '0' && b <= '9':
			oct = oct*10 + uint32(b-'0')
			if oct > 255 {
				return 0, ErrBadIP
			}
		default:
			return 0, ErrBadIP
		}
	}
	if num != 3 {
		return 0, ErrBadIP
	}
	if 0xffffffff-ip < oct {
		return 0, ErrBadIP
	}
	return ip<<8 + oct, nil
}

func parsecidr4(cidr []byte) (uint32, uint32, error) {
	var mask uint32
	p := bytes.IndexByte(cidr, '/')
	if p > 0 {
		for _, c := range cidr[p+1:] {
			if c < '0' || c > '9' {
				return 0, 0, ErrBadIP
			}
			mask = mask*10 + uint32(c-'0')
		}
		mask = 0xffffffff << (32 - mask)
		cidr = cidr[:p]
	} else {
		mask = 0xffffffff
	}
	ip, err := loadip4(cidr)
	if err != nil {
		return 0, 0, err
	}
	return ip, mask, nil
}

func parsecidr6(cidr []byte) (net.IP, net.IPMask, error) {
	p := bytes.IndexByte(cidr, '/')
	if p > 0 {
		_, ipm, err := net.ParseCIDR(string(cidr))
		if err != nil {
			return nil, nil, err
		}
		return ipm.IP, ipm.Mask, nil
	}
	ip := net.ParseIP(string(cidr))
	if ip == nil {
		return nil, nil, ErrBadIP
	}
	return ip, net.IPMask{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, nil
}
