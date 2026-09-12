package catalog

import (
	"strings"
	"testing"
)

const lenovoFixture = `<ModelList version="1.0">
 <Model name="ThinkCentre M70q Gen 3" arch="Intel">
  <Types><Type>11T3</Type><Type>11T4</Type></Types>
  <BIOS version="M3BKT1" image="m3bk" date="2024-01-01" crc="aa" md5="bb">https://download.lenovo.com/bios.exe</BIOS>
  <SCCM os="win10" version="22H2" date="2023-05-01" crc="1111111111111111111111111111111111111111111111111111111111111111" md5="x">https://download.lenovo.com/pccbbs/thinkcentre_drivers/tc_m70q_w1064_22h2.exe</SCCM>
  <SCCM os="win11" version="22H2" date="2023-06-01" crc="2222222222222222222222222222222222222222222222222222222222222222" md5="y">https://download.lenovo.com/pccbbs/thinkcentre_drivers/tc_m70q_w11_22h2.exe</SCCM>
  <SCCM os="win11" version="23H2" date="2024-02-01" crc="3333333333333333333333333333333333333333333333333333333333333333" md5="z">https://download.lenovo.com/pccbbs/thinkcentre_drivers/tc_m70q_w11_23h2.exe</SCCM>
 </Model>
 <Model name="ThinkPad T14 Gen 4" arch="Intel">
  <Types><Type>21HD</Type></Types>
  <SCCM os="win11" version="*" date="2024-03-01" crc="4444444444444444444444444444444444444444444444444444444444444444" md5="w">https://download.lenovo.com/pccbbs/mobiles/tp_t14g4_w11.exe</SCCM>
 </Model>
</ModelList>`

func TestParseLenovo(t *testing.T) {
	packs, err := parseLenovo([]byte(lenovoFixture), Query{Model: "m70q gen 3", OS: "win11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Fatalf("got %d packs, want 2 win11 packs: %+v", len(packs), packs)
	}
	if packs[0].OSVersion != "23H2" {
		t.Errorf("newest first: got %s", packs[0].OSVersion)
	}
	if packs[0].SHA256 != strings.Repeat("3", 64) || packs[0].Format != "exe" || len(packs[0].Extract) == 0 {
		t.Errorf("pack fields: %+v", packs[0])
	}
	if packs[0].ID() != "lenovo-thinkcentre-m70q-gen-3-win11-23h2" {
		t.Errorf("id = %s", packs[0].ID())
	}
	// Machine type lookup.
	byType, _ := parseLenovo([]byte(lenovoFixture), Query{Model: "21HD", OS: "win11"})
	if len(byType) != 1 || byType[0].Model != "ThinkPad T14 Gen 4" {
		t.Errorf("machine-type lookup: %+v", byType)
	}
}

const dellFixture = `<DriverPackManifest baseLocation="downloads.dell.com">
<DriverPackage format="cab" hashMD5="AA" size="1000" dateTime="2025-03-10T00:00:00" vendorVersion="1.0" dellVersion="A05" path="FOLDER1/1/7010-win11-A05.CAB" releaseID="ABC" type="win">
 <Name><Display lang="en"><![CDATA[7010-win11-A05.CAB]]></Display></Name>
 <SupportedOperatingSystems><OperatingSystem osCode="Windows11" osArch="x64"><Display lang="en"><![CDATA[Windows 11 x64]]></Display></OperatingSystem></SupportedOperatingSystems>
 <SupportedSystems><Brand key="1" prefix="OP"><Display lang="en"><![CDATA[ Optiplex ]]></Display><Model systemID="0B12" name="OptiPlex 7010"><Display lang="en"><![CDATA[ 7010 ]]></Display></Model></Brand></SupportedSystems>
 <Cryptography><Hash algorithm="MD5">AA</Hash><Hash algorithm="SHA1">BB</Hash><Hash algorithm="SHA256">CC00</Hash></Cryptography>
 <DriverPackMetadataInfo><Size>1</Size><Cryptography><Hash algorithm="SHA256">DEADBEEF</Hash></Cryptography></DriverPackMetadataInfo>
</DriverPackage>
<DriverPackage format="exe" size="1001" dateTime="2025-03-10T00:00:00" vendorVersion="1.0" dellVersion="A05" path="FOLDER1/1/7010-win11-A05.exe" releaseID="ABD" type="win">
 <Name><Display lang="en"><![CDATA[7010-win11-A05.exe]]></Display></Name>
 <SupportedOperatingSystems><OperatingSystem osCode="Windows11" osArch="x64"/></SupportedOperatingSystems>
 <SupportedSystems><Brand key="1" prefix="OP"><Model systemID="0B12" name="OptiPlex 7010"/></Brand></SupportedSystems>
 <Cryptography><Hash algorithm="SHA256">CC01</Hash></Cryptography>
</DriverPackage>
<DriverPackage format="cab" size="999" dateTime="2019-01-01T00:00:00" vendorVersion="1.0" dellVersion="A01" path="FOLDER0/1/7010-win10-A01.CAB" releaseID="OLD" type="win">
 <SupportedOperatingSystems><OperatingSystem osCode="Windows10" osArch="x64"/></SupportedOperatingSystems>
 <SupportedSystems><Brand key="1" prefix="OP"><Model systemID="0B12" name="OptiPlex 7010"/></Brand></SupportedSystems>
 <Cryptography><Hash algorithm="SHA256">CC02</Hash></Cryptography>
</DriverPackage>
</DriverPackManifest>`

func TestParseDell(t *testing.T) {
	packs, err := parseDell([]byte(dellFixture), Query{Model: "optiplex 7010", OS: "win11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Fatalf("got %d win11 packs, want 2: %+v", len(packs), packs)
	}
	if packs[0].Format != "cab" {
		t.Errorf("cab should be preferred over exe on equal dates, got %s", packs[0].Format)
	}
	if packs[0].SHA256 != "cc00" {
		t.Errorf("package-level SHA256 = %q (must not pick the metadata-info hash)", packs[0].SHA256)
	}
	if packs[0].URL != "https://downloads.dell.com/FOLDER1/1/7010-win11-A05.CAB" {
		t.Errorf("url = %s", packs[0].URL)
	}
	if packs[1].Format != "exe" || len(packs[1].Extract) == 0 {
		t.Errorf("exe pack should carry extract args: %+v", packs[1])
	}
	bySystemID, _ := parseDell([]byte(dellFixture), Query{Model: "0B12", OS: "win10"})
	if len(bySystemID) != 1 || bySystemID[0].Released != "2019-01-01" {
		t.Errorf("system-id / win10 lookup: %+v", bySystemID)
	}
}

const hpFixture = `<NewDataSet><HPClientDriverPackCatalog>
<ProductOSDriverPackList>
 <ProductOSDriverPack><Architecture>64-bit</Architecture><ProductType>Desktops</ProductType><SystemId>8b2f,8b30</SystemId><SystemName>HP EliteDesk 800 G9 Desktop Mini PC</SystemName><OSName>Windows 11 64-bit, 23H2</OSName><OSId>4261</OSId><SoftPaqId>sp150000</SoftPaqId></ProductOSDriverPack>
 <ProductOSDriverPack><Architecture>64-bit</Architecture><ProductType>Desktops</ProductType><SystemId>8b2f,8b30</SystemId><SystemName>HP EliteDesk 800 G9 Desktop Mini PC</SystemName><OSName>Windows 11 64-bit, 22H2</OSName><OSId>4260</OSId><SoftPaqId>sp149000</SoftPaqId></ProductOSDriverPack>
 <ProductOSDriverPack><Architecture>64-bit</Architecture><ProductType>Desktops</ProductType><SystemId>8b2f</SystemId><SystemName>HP EliteDesk 800 G9 Desktop Mini PC</SystemName><OSName>Windows 10 64-bit, 22H2</OSName><OSId>4240</OSId><SoftPaqId>sp148000</SoftPaqId></ProductOSDriverPack>
</ProductOSDriverPackList>
<SoftPaqList>
 <SoftPaq><Id>sp150000</Id><Name>HP EliteDesk 800 G9 Win11 Driver Pack</Name><Version>3.00 A 1</Version><Category>Manageability - Driver Pack</Category><DateReleased>4/15/2025 12:00:00 AM</DateReleased><Url>ftp://ftp.hp.com/pub/softpaq/sp150001-150500/sp150000.exe</Url><Size>900000000</Size><MD5>md5a</MD5><SHA256>ABCDEF</SHA256></SoftPaq>
 <SoftPaq><Id>sp149000</Id><Name>HP EliteDesk 800 G9 Win11 Driver Pack</Name><Version>2.00 A 1</Version><Category>Manageability - Driver Pack</Category><DateReleased>1/10/2024 12:00:00 AM</DateReleased><Url>ftp://ftp.hp.com/pub/softpaq/sp148501-149000/sp149000.exe</Url><Size>800000000</Size><MD5>md5b</MD5><SHA256>123456</SHA256></SoftPaq>
 <SoftPaq><Id>sp148000</Id><Name>old</Name><Version>1</Version><DateReleased>1/1/2023 12:00:00 AM</DateReleased><Url>ftp://ftp.hp.com/pub/softpaq/sp147501-148000/sp148000.exe</Url><Size>1</Size><SHA256>000</SHA256></SoftPaq>
</SoftPaqList>
</HPClientDriverPackCatalog></NewDataSet>`

func TestParseHP(t *testing.T) {
	packs, err := parseHP([]byte(hpFixture), Query{Model: "elitedesk 800 g9", OS: "win11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 2 {
		t.Fatalf("got %d win11 packs, want 2: %+v", len(packs), packs)
	}
	if packs[0].OSVersion != "23H2" || packs[0].SHA256 != "abcdef" {
		t.Errorf("newest first / hash: %+v", packs[0])
	}
	if !strings.HasPrefix(packs[0].URL, "https://ftp.hp.com/") {
		t.Errorf("ftp URL should be rewritten to https: %s", packs[0].URL)
	}
	if packs[0].Released != "2025-04-15" {
		t.Errorf("released = %s", packs[0].Released)
	}
	if len(packs[0].Extract) == 0 || packs[0].Format != "exe" {
		t.Errorf("softpaq extract args missing: %+v", packs[0])
	}
}

const msRow = `<tr id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_R0" style="border-width:0px;"> <td class="resultsbottomBorder" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C0_R0"> </td><td class="resultsbottomBorder resultspadding" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C1_R0"> <a id='9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_link' href= "javascript:void(0);" onclick='goToDetails("9d6cf3f1-6313-42ec-95fd-7d84b59e6e49");' class="contentTextItemSpacerNoBreakLink"> Intel Net Driver Update (12.19.2.66) </a> </td><td class="resultsbottomBorder resultspadding" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C2_R0"> Windows 11 Client, version 24H2 and later, Servicing Drivers </td><td class="resultsbottomBorder resultspadding" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C3_R0"> Drivers (Networking) </td><td class="resultsbottomBorder resultspadding " id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C4_R0"> 11/15/2025 </td><td class="resultsbottomBorder resultspadding" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C5_R0"> 12.19.2.66 </td><td class="resultsbottomBorder resultspadding resultsSizeWidth" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C6_R0"> <span id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_size">279 KB</span> <span class="noDisplay" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_originalSize">286663</span> </td><td class="resultsbottomBorder resultsButtonWidth" id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49_C7_R0"> <input id="9d6cf3f1-6313-42ec-95fd-7d84b59e6e49" class="flatBlueButtonDownload focus-only" type="button" value='Download' /> </td> </tr>
<tr id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_R1"><td id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_C1_R1">Some Update</td><td id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_C2_R1">Windows 10</td><td id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_C3_R1">Updates</td><td id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_C4_R1">1/1/2020</td><td id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_C5_R1">1</td><td id="aaaaaaaa-6313-42ec-95fd-7d84b59e6e49_C6_R1">1</td></tr>`

func TestParseMSCatalog(t *testing.T) {
	packs := parseMSCatalog(msRow, Query{HWID: `PCI\VEN_8086&DEV_15B8`, OS: "win11"})
	if len(packs) != 1 {
		t.Fatalf("got %d driver rows, want 1 (non-driver row filtered): %+v", len(packs), packs)
	}
	p := packs[0]
	if p.UpdateID != "9d6cf3f1-6313-42ec-95fd-7d84b59e6e49" || p.Model != "Intel Net Driver Update (12.19.2.66)" {
		t.Errorf("row parse: %+v", p)
	}
	if p.Version != "12.19.2.66" || p.Released != "2025-11-15" || p.Size != 286663 {
		t.Errorf("fields: version=%s released=%s size=%d", p.Version, p.Released, p.Size)
	}
}
