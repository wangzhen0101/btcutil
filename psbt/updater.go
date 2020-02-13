// Copyright (c) 2018 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package psbt

// The Updater requires provision of a single PSBT and is able to add data to
// both input and output sections.  It can be called repeatedly to add more
// data.  It also allows addition of signatures via the addPartialSignature
// function; this is called internally to the package in the Sign() function of
// Updater, located in signer.go

import (
	"bytes"
	"crypto/sha256"

	"github.com/wangzhen0101/wzbtc/txscript"
	"github.com/wangzhen0101/wzbtc/wire"
	"github.com/wangzhen0101/btcutil"
)

// Updater encapsulates the role 'Updater' as specified in BIP174; it accepts
// Psbt structs and has methods to add fields to the inputs and outputs.
type Updater struct {
	Upsbt *Packet
}

// NewUpdater returns a new instance of Updater, if the passed Psbt struct is
// in a valid form, else an error.
func NewUpdater(p *Packet) (*Updater, error) {
	if err := p.SanityCheck(); err != nil {
		return nil, err
	}

	return &Updater{Upsbt: p}, nil

}

// AddInNonWitnessUtxo adds the utxo information for an input which is
// non-witness. This requires provision of a full transaction (which is the
// source of the corresponding prevOut), and the input index. If addition of
// this key-value pair to the Psbt fails, an error is returned.
func (p *Updater) AddInNonWitnessUtxo(tx *wire.MsgTx, inIndex int) error {
	if inIndex > len(p.Upsbt.Inputs)-1 {
		return ErrInvalidPrevOutNonWitnessTransaction
	}

	p.Upsbt.Inputs[inIndex].NonWitnessUtxo = tx

	if err := p.Upsbt.SanityCheck(); err != nil {
		return ErrInvalidPsbtFormat
	}

	return nil
}

// AddInWitnessUtxo adds the utxo information for an input which is witness.
// This requires provision of a full transaction *output* (which is the source
// of the corresponding prevOut); not the full transaction because BIP143 means
// the output information is sufficient, and the input index. If addition of
// this key-value pair to the Psbt fails, an error is returned.
func (p *Updater) AddInWitnessUtxo(txout *wire.TxOut, inIndex int) error {
	if inIndex > len(p.Upsbt.Inputs)-1 {
		return ErrInvalidPsbtFormat
	}

	p.Upsbt.Inputs[inIndex].WitnessUtxo = txout

	if err := p.Upsbt.SanityCheck(); err != nil {
		return ErrInvalidPsbtFormat
	}

	return nil
}

// addPartialSignature allows the Updater role to insert fields of type partial
// signature into a Psbt, consisting of both the pubkey (as keydata) and the
// ECDSA signature (as value).  Note that the Signer role is encapsulated in
// this function; signatures are only allowed to be added that follow the
// sanity-check on signing rules explained in the BIP under `Signer`; if the
// rules are not satisfied, an ErrInvalidSignatureForInput is returned.
//
// NOTE: This function does *not* validate the ECDSA signature itself.
func (p *Updater) addPartialSignature(inIndex int, sig []byte,
	pubkey []byte) error {

	partialSig := PartialSig{
		PubKey: pubkey, Signature: sig,
	}

	// First validate the passed (sig, pub).
	if !partialSig.checkValid() {
		return ErrInvalidPsbtFormat
	}

	pInput := p.Upsbt.Inputs[inIndex]

	// First check; don't add duplicates.
	for _, x := range pInput.PartialSigs {
		if bytes.Equal(x.PubKey, partialSig.PubKey) {
			return ErrDuplicateKey
		}
	}

	// Next, we perform a series of additional sanity checks.
	if pInput.NonWitnessUtxo != nil {
		if len(p.Upsbt.UnsignedTx.TxIn) < inIndex+1 {
			return ErrInvalidPrevOutNonWitnessTransaction
		}

		if pInput.NonWitnessUtxo.TxHash() !=
			p.Upsbt.UnsignedTx.TxIn[inIndex].PreviousOutPoint.Hash {
			return ErrInvalidSignatureForInput
		}

		// To validate that the redeem script matches, we must pull out
		// the scriptPubKey of the corresponding output and compare
		// that with the P2SH scriptPubKey that is generated by
		// redeemScript.
		if pInput.RedeemScript != nil {
			outIndex := p.Upsbt.UnsignedTx.TxIn[inIndex].PreviousOutPoint.Index
			scriptPubKey := pInput.NonWitnessUtxo.TxOut[outIndex].PkScript
			scriptHash := btcutil.Hash160(pInput.RedeemScript)

			scriptHashScript, err := txscript.NewScriptBuilder().
				AddOp(txscript.OP_HASH160).
				AddData(scriptHash).
				AddOp(txscript.OP_EQUAL).
				Script()
			if err != nil {
				return err
			}

			if !bytes.Equal(scriptHashScript, scriptPubKey) {
				return ErrInvalidSignatureForInput
			}
		}

	} else if pInput.WitnessUtxo != nil {
		scriptPubKey := pInput.WitnessUtxo.PkScript

		var script []byte
		if pInput.RedeemScript != nil {
			scriptHash := btcutil.Hash160(pInput.RedeemScript)
			scriptHashScript, err := txscript.NewScriptBuilder().
				AddOp(txscript.OP_HASH160).
				AddData(scriptHash).
				AddOp(txscript.OP_EQUAL).
				Script()
			if err != nil {
				return err
			}

			if !bytes.Equal(scriptHashScript, scriptPubKey) {
				return ErrInvalidSignatureForInput
			}

			script = pInput.RedeemScript
		} else {
			script = scriptPubKey
		}

		// If a witnessScript field is present, this is a P2WSH,
		// whether nested or not (that is handled by the assignment to
		// `script` above); in that case, sanity check that `script` is
		// the p2wsh of witnessScript. Contrariwise, if no
		// witnessScript field is present, this will be signed as
		// p2wkh.
		if pInput.WitnessScript != nil {
			witnessScriptHash := sha256.Sum256(pInput.WitnessScript)
			witnessScriptHashScript, err := txscript.NewScriptBuilder().
				AddOp(txscript.OP_0).
				AddData(witnessScriptHash[:]).
				Script()
			if err != nil {
				return err
			}

			if !bytes.Equal(script, witnessScriptHashScript[:]) {
				return ErrInvalidSignatureForInput
			}
		} else {
			// Otherwise, this is a p2wkh input.
			pubkeyHash := btcutil.Hash160(pubkey)
			pubkeyHashScript, err := txscript.NewScriptBuilder().
				AddOp(txscript.OP_0).
				AddData(pubkeyHash).
				Script()
			if err != nil {
				return err
			}

			// Validate that we're able to properly reconstruct the
			// witness program.
			if !bytes.Equal(pubkeyHashScript, script) {
				return ErrInvalidSignatureForInput
			}
		}
	} else {

		// Attaching signature without utxo field is not allowed.
		return ErrInvalidPsbtFormat
	}

	p.Upsbt.Inputs[inIndex].PartialSigs = append(
		p.Upsbt.Inputs[inIndex].PartialSigs, &partialSig,
	)

	if err := p.Upsbt.SanityCheck(); err != nil {
		return err
	}

	// Addition of a non-duplicate-key partial signature cannot violate
	// sanity-check rules.
	return nil
}

// AddInSighashType adds the sighash type information for an input.  The
// sighash type is passed as a 32 bit unsigned integer, along with the index
// for the input. An error is returned if addition of this key-value pair to
// the Psbt fails.
func (p *Updater) AddInSighashType(sighashType txscript.SigHashType,
	inIndex int) error {

	p.Upsbt.Inputs[inIndex].SighashType = sighashType

	if err := p.Upsbt.SanityCheck(); err != nil {
		return err
	}
	return nil
}

// AddInRedeemScript adds the redeem script information for an input.  The
// redeem script is passed serialized, as a byte slice, along with the index of
// the input. An error is returned if addition of this key-value pair to the
// Psbt fails.
func (p *Updater) AddInRedeemScript(redeemScript []byte,
	inIndex int) error {

	p.Upsbt.Inputs[inIndex].RedeemScript = redeemScript

	if err := p.Upsbt.SanityCheck(); err != nil {
		return ErrInvalidPsbtFormat
	}

	return nil
}

// AddInWitnessScript adds the witness script information for an input.  The
// witness script is passed serialized, as a byte slice, along with the index
// of the input. An error is returned if addition of this key-value pair to the
// Psbt fails.
func (p *Updater) AddInWitnessScript(witnessScript []byte,
	inIndex int) error {

	p.Upsbt.Inputs[inIndex].WitnessScript = witnessScript

	if err := p.Upsbt.SanityCheck(); err != nil {
		return err
	}

	return nil
}

// AddInBip32Derivation takes a master key fingerprint as defined in BIP32, a
// BIP32 path as a slice of uint32 values, and a serialized pubkey as a byte
// slice, along with the integer index of the input, and inserts this data into
// that input.
//
// NOTE: This can be called multiple times for the same input.  An error is
// returned if addition of this key-value pair to the Psbt fails.
func (p *Updater) AddInBip32Derivation(masterKeyFingerprint uint32,
	bip32Path []uint32, pubKeyData []byte, inIndex int) error {

	bip32Derivation := Bip32Derivation{
		PubKey:               pubKeyData,
		MasterKeyFingerprint: masterKeyFingerprint,
		Bip32Path:            bip32Path,
	}

	if !bip32Derivation.checkValid() {
		return ErrInvalidPsbtFormat
	}

	// Don't allow duplicate keys
	for _, x := range p.Upsbt.Inputs[inIndex].Bip32Derivation {
		if bytes.Equal(x.PubKey, bip32Derivation.PubKey) {
			return ErrDuplicateKey
		}
	}

	p.Upsbt.Inputs[inIndex].Bip32Derivation = append(
		p.Upsbt.Inputs[inIndex].Bip32Derivation, &bip32Derivation,
	)

	if err := p.Upsbt.SanityCheck(); err != nil {
		return err
	}

	return nil
}

// AddOutBip32Derivation takes a master key fingerprint as defined in BIP32, a
// BIP32 path as a slice of uint32 values, and a serialized pubkey as a byte
// slice, along with the integer index of the output, and inserts this data
// into that output.
//
// NOTE: That this can be called multiple times for the same output.  An error
// is returned if addition of this key-value pair to the Psbt fails.
func (p *Updater) AddOutBip32Derivation(masterKeyFingerprint uint32,
	bip32Path []uint32, pubKeyData []byte, outIndex int) error {

	bip32Derivation := Bip32Derivation{
		PubKey:               pubKeyData,
		MasterKeyFingerprint: masterKeyFingerprint,
		Bip32Path:            bip32Path,
	}

	if !bip32Derivation.checkValid() {
		return ErrInvalidPsbtFormat
	}

	// Don't allow duplicate keys
	for _, x := range p.Upsbt.Outputs[outIndex].Bip32Derivation {
		if bytes.Equal(x.PubKey, bip32Derivation.PubKey) {
			return ErrDuplicateKey
		}
	}

	p.Upsbt.Outputs[outIndex].Bip32Derivation = append(
		p.Upsbt.Outputs[outIndex].Bip32Derivation, &bip32Derivation,
	)

	if err := p.Upsbt.SanityCheck(); err != nil {
		return err
	}

	return nil
}

// AddOutRedeemScript takes a redeem script as a byte slice and appends it to
// the output at index outIndex.
func (p *Updater) AddOutRedeemScript(redeemScript []byte,
	outIndex int) error {

	p.Upsbt.Outputs[outIndex].RedeemScript = redeemScript

	if err := p.Upsbt.SanityCheck(); err != nil {
		return ErrInvalidPsbtFormat
	}

	return nil
}

// AddOutWitnessScript takes a witness script as a byte slice and appends it to
// the output at index outIndex.
func (p *Updater) AddOutWitnessScript(witnessScript []byte,
	outIndex int) error {

	p.Upsbt.Outputs[outIndex].WitnessScript = witnessScript

	if err := p.Upsbt.SanityCheck(); err != nil {
		return err
	}

	return nil
}
